package wechatdraft

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
	"golang.org/x/net/html"
)

var allowedTags = wordSet("section div p span br h1 h2 h3 h4 h5 h6 strong b em i u s blockquote ul ol li hr a img table thead tbody tr th td pre code sup sub figure figcaption")
var allowedAttrs = wordSet("style title alt href src width height colspan rowspan align class id data-no-dark data-ignore-dm")

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, word := range strings.Fields(s) {
		m[word] = true
	}
	return m
}

// ProcessHTML rejects unsupported input rather than silently deleting it.
// The original snapshot is preserved; only the final body is rendered.
func ProcessHTML(source string, images []v1.Image, urls map[string]string) (string, *APIError) {
	if !utf8.ValidString(source) {
		return "", htmlFault("正文包含无效字符", "contentHtml")
	}
	if strings.TrimSpace(source) == "" {
		return "", htmlFault("正文不能为空", "contentHtml")
	}
	// Token validation prevents parser repair from discarding illegal elements/attributes.
	z := html.NewTokenizer(strings.NewReader(source))
	for {
		t := z.Next()
		if t == html.ErrorToken {
			if z.Err() != io.EOF {
				return "", htmlFault("HTML 解析失败", "contentHtml")
			}
			break
		}
		if t == html.DoctypeToken {
			return "", htmlFault("正文不支持文档声明", "contentHtml")
		}
		if t != html.StartTagToken && t != html.SelfClosingTagToken && t != html.EndTagToken {
			continue
		}
		tok := z.Token()
		if !allowedTags[tok.Data] {
			return "", htmlFault("不支持的标签", tok.Data)
		}
		seen := map[string]bool{}
		for _, attr := range tok.Attr {
			if seen[attr.Key] || attr.Namespace != "" || !allowedAttrs[attr.Key] {
				return "", htmlFault("不支持的属性", attr.Key)
			}
			seen[attr.Key] = true
			if attr.Key == "href" && (tok.Data != "a" || !webURL(attr.Val)) {
				return "", htmlFault("非法链接", tok.Data)
			}
			if attr.Key == "src" && tok.Data != "img" {
				return "", htmlFault("非法资源引用", tok.Data)
			}
		}
	}
	nodes, err := parseFragment(source)
	if err != nil {
		return "", htmlFault("HTML 解析失败", "contentHtml")
	}
	body := map[string]bool{}
	for _, im := range images {
		if im.Role == "body" {
			body[im.ID] = true
		}
	}
	used := map[string]bool{}
	resolve := func(resource string) (string, *APIError) {
		id := strings.TrimPrefix(resource, "cw-image://")
		if resource != "cw-image://"+id || !body[id] {
			return "", imageFault("image_not_declared", "正文图片引用未声明")
		}
		used[id] = true
		if urls == nil {
			return resource, nil
		}
		if !webURL(urls[id]) {
			return "", imageFault("image_wechat_url_missing", "正文图片缺少微信地址")
		}
		return urls[id], nil
	}
	var walk func(*html.Node, int) *APIError
	walk = func(n *html.Node, depth int) *APIError {
		if depth > 64 {
			return htmlFault("HTML 嵌套过深", "contentHtml")
		}
		for i, attr := range n.Attr {
			if attr.Key == "style" {
				style, e := backgroundStyle(attr.Val, resolve)
				if e != nil {
					return e
				}
				n.Attr[i].Val = style
			}
		}
		if n.Type == html.ElementNode && n.Data == "img" {
			found := false
			for i, a := range n.Attr {
				if a.Key != "src" {
					continue
				}
				value, e := resolve(a.Val)
				if e != nil {
					return e
				}
				n.Attr[i].Val, found = value, true
			}
			if !found {
				return imageFault("image_src_missing", "正文图片缺少 src")
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if e := walk(child, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	for _, n := range nodes {
		if e := walk(n, 0); e != nil {
			return "", e
		}
	}
	if len(used) != len(body) {
		return "", imageFault("image_unused", "声明的正文图片未被正文引用")
	}
	if urls == nil {
		return source, nil
	}
	var buf bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&buf, n); err != nil {
			return "", htmlFault("HTML 生成失败", "contentHtml")
		}
	}
	// WeChat clears a whole style containing numeric quote entities emitted
	// by html.Render. Keep double-quoted HTML attributes intact while using
	// CSS-compatible apostrophes and named double-quote entities.
	content := wechatHTMLQuotes.Replace(buf.String())
	return content, nil
}

var wechatHTMLQuotes = strings.NewReplacer("&#39;", "'", "&#34;", "&quot;")

func htmlFault(reason, position string) *APIError {
	e := fail(422, "VALIDATION_FAILED", reason)
	// Only parser-controlled tag/attribute names are returned.
	if !allowedTags[position] {
		position = "contentHtml"
	}
	e.Fault.Details = map[string]string{"position": fmt.Sprintf("%.64s", position), "reason": htmlReasons[reason]}
	return e
}

var htmlReasons = map[string]string{
	"正文不能为空": "html_empty", "正文包含无效字符": "html_encoding", "HTML 解析失败": "html_parse",
	"正文不支持文档声明": "html_doctype", "不支持的标签": "html_tag",
	"不支持的属性": "html_attribute", "不支持的内联样式": "html_style",
	"非法链接": "html_link", "非法资源引用": "html_resource",
	"HTML 嵌套过深": "html_depth", "HTML 生成失败": "html_render",
}

func imageFault(reason, message string) *APIError {
	e := fail(422, "IMAGE_REFERENCE_INVALID", message)
	e.Fault.Details = map[string]string{"reason": reason}
	return e
}
