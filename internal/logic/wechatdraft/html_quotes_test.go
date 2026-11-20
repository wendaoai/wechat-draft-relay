package wechatdraft

import (
	"strings"
	"testing"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
	"golang.org/x/net/html"
)

func TestWechatQuotedStylesUseCompatibleHTMLEntities(t *testing.T) {
	source := `<section style="background-color:#e8f3ff;background-image:url(&quot;cw-image://paper&quot;)"><h2 style="background-color:#1265df;color:#fff;font-family:'PingFang SC','Microsoft YaHei',sans-serif">蓝色标题</h2></section>`
	images := []v1.Image{{ID: "paper", Role: "body"}}
	prepared, fault := ProcessHTML(source, images, nil)
	if fault != nil || prepared != source {
		t.Fatalf("original snapshot changed: %v", fault)
	}
	output, fault := ProcessHTML(source, images, map[string]string{"paper": "https://mmbiz.qpic.cn/paper.png?a=1&b=2"})
	if fault != nil {
		t.Fatal(fault)
	}
	if strings.Contains(output, "&#39;") || strings.Contains(output, "&#34;") ||
		!strings.Contains(output, "'PingFang SC'") || !strings.Contains(output, `url(&quot;https://mmbiz.qpic.cn/`) {
		t.Fatalf("numeric quotes make WeChat clear the entire style: %s", output)
	}
	nodes, err := parseFragment(output)
	if err != nil || len(nodes) != 1 || len(nodes[0].Attr) != 1 ||
		!strings.Contains(nodes[0].Attr[0].Val, `url("https://mmbiz.qpic.cn/paper.png?a=1&b=2")`) {
		t.Fatalf("background URL or HTML attribute boundary changed: %v %s", err, output)
	}
}

func TestWechatQuoteEncodingKeepsTextAndAttributesInert(t *testing.T) {
	source := `<p title="&quot; onmouseover=&quot;bad&quot; ' ">&lt;img src=x onerror=bad&gt; &amp;#34; &amp;#39; &quot; '</p>`
	output, fault := ProcessHTML(source, nil, map[string]string{})
	if fault != nil {
		t.Fatal(fault)
	}
	parse := func(value string) *html.Node {
		t.Helper()
		nodes, err := parseFragment(value)
		if err != nil || len(nodes) != 1 {
			t.Fatalf("invalid output: %v %s", err, value)
		}
		return nodes[0]
	}
	before, after := parse(source), parse(output)
	if len(after.Attr) != 1 || after.Attr[0] != before.Attr[0] || after.FirstChild == nil ||
		after.FirstChild.Type != html.TextNode || after.FirstChild.NextSibling != nil || after.FirstChild.Data != before.FirstChild.Data {
		t.Fatalf("quote normalization changed HTML semantics: %s", output)
	}
}
