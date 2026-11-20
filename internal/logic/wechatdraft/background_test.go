package wechatdraft

import (
	"strings"
	"testing"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func TestBackgroundReferencesAndFinalReplacement(t *testing.T) {
	source := `<section style="background:linear-gradient(#fff,#eee),URL('cw-image://leaf') center/cover no-repeat; padding: 12px"><p style="background-image:url(&quot;cw-image://paper&quot;),url(cw-image://leaf)">正文</p><img src="cw-image://leaf"></section>`
	images := []v1.Image{{ID: "leaf", Role: "body"}, {ID: "paper", Role: "body"}}
	prepared, e := ProcessHTML(source, images, nil)
	if e != nil || prepared != source {
		t.Fatalf("original snapshot altered or rejected: %v", e)
	}
	final, e := ProcessHTML(source, images, map[string]string{"leaf": "https://mmbiz.qpic.cn/leaf.gif?a=1&b=2", "paper": "https://mmbiz.qpic.cn/paper.png"})
	if e != nil || strings.Contains(final, "cw-image:") || strings.Count(final, "https://mmbiz.qpic.cn/leaf.gif") != 3 || !strings.Contains(final, "paper.png") || !strings.Contains(final, "linear-gradient") || !strings.Contains(final, "padding: 12px") {
		t.Fatalf("lost layout or resources: %v %s", e, final)
	}
	for _, urls := range []map[string]string{{}, {"leaf": "javascript:bad", "paper": "https://mmbiz.qpic.cn/paper.png"}, {"leaf": "https://example.com/\";color:red", "paper": "https://mmbiz.qpic.cn/paper.png"}} {
		if _, e := ProcessHTML(source, images, urls); e == nil {
			t.Fatal("accepted missing/unsafe final image URL")
		}
	}
}

func TestBackgroundPolicyCannotLoadUnregisteredResources(t *testing.T) {
	for _, style := range []string{
		`background-image:url(https://example.com/a)`, `background:url(data:image/png;base64,eA==)`,
		`background:url(javascript:bad)`, `background:url(cw-image://missing)`, `background:url(cw-image://cover)`,
		`cursor:url(cw-image://body)`, `background-color:url(cw-image://body)`,
		`--asset:url(cw-image://body);background:var(--asset)`,
		`--asset:url/**/(cw-image://body);background:var(--asset)`,
		`background:u\72l(cw-image://body)`, `background:url(/*comment*/cw-image://body)`,
		`background:image-set('https://example.com/a' 1x)`, `background:paint(remote)`,
		`width:expression(alert(1))`, `behavior:none`, `-moz-binding:none`,
	} {
		t.Run(style, func(t *testing.T) {
			source := `<section style="` + strings.ReplaceAll(style, `"`, "&quot;") + `">正文<img src="cw-image://body"></section>`
			if _, e := ProcessHTML(source, []v1.Image{{ID: "body", Role: "body"}, {ID: "cover", Role: "cover"}}, nil); e == nil {
				t.Fatal("accepted unsafe style")
			}
		})
	}
	if _, e := ProcessHTML(`<p>正文</p>`, []v1.Image{{ID: "body", Role: "body"}}, nil); e == nil || e.Fault.Details.(map[string]string)["reason"] != "image_unused" {
		t.Fatal("unused image accepted or reason lost")
	}
}
