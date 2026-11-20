package wechatdraft_test

import (
	"strings"
	"testing"
)

func TestHTTPNamedColorsSurviveDraftRendering(t *testing.T) {
	for _, color := range []string{"coral", "rebeccapurple", "whitesmoke", "papayawhip", "unlisted-color"} {
		t.Run(color, func(t *testing.T) {
			w := newWX(t)
			h := startHarness(t, w, config(t))
			p, token, files := payload("wx0123456789abcdef", true)
			p.Article.ContentHTML = `<p style="color: ` + color + `; background-color: ` + color + `">正文</p><img src="cw-image://body">`
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 202, "")
			h.wait(p, token, "succeeded")
			w.mu.Lock()
			defer w.mu.Unlock()
			if len(w.contents) != 1 || !strings.Contains(w.contents[0], "color: "+color) || !strings.Contains(w.contents[0], "background-color: "+color) || !strings.Contains(w.contents[0], "https://mmbiz.qpic.cn/test/image.png") {
				t.Fatal("rendered draft lost permitted color or image", w.contents)
			}
		})
	}
	for _, style := range []string{"color: url(https://example.invalid/resource)", `background: image-set("https://example.invalid/image.png" 1x)`, "width: expression(alert(1))", "behavior: none", "--asset: url/**/(https://example.invalid/image.png); background: var(--asset)"} {
		t.Run(style, func(t *testing.T) {
			w := newWX(t)
			h := startHarness(t, w, config(t))
			p, token, files := payload("wx0123456789abcdef", false)
			p.Article.ContentHTML = `<p style="` + style + `">正文</p>`
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 422, "VALIDATION_FAILED")
			tokens, covers, bodies, drafts := w.counts()
			if tokens+covers+bodies+drafts != 0 {
				t.Fatal("invalid style reached WeChat")
			}
		})
	}
}
