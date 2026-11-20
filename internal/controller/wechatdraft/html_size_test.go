package wechatdraft_test

import (
	"strings"
	"testing"
)

func TestHTTPLargeHTMLPushAndRestart(t *testing.T) {
	for _, size := range []int{20000, (1<<20)/3 + 1} {
		c := config(t)
		w := newWX(t)
		h := startHarness(t, w, c)
		p, token, files := payload("wx0123456789abcdef", true)
		text := strings.Repeat("中", size)
		p.Article.ContentHTML = `<section><p>` + text + `</p><img src="cw-image://body"></section>`
		status, out := h.post(p, token, files, false)
		expect(t, status, out, 202, "")
		h.wait(p, token, "succeeded")
		w.mu.Lock()
		preserved := len(w.contents) == 1 && strings.Contains(w.contents[0], text) && strings.Contains(w.contents[0], "https://mmbiz.qpic.cn/test/image.png")
		w.mu.Unlock()
		if !preserved {
			t.Fatal("large HTML was truncated or image substitution failed")
		}
		h.close()
		h = startHarness(t, w, c)
		status, out = h.get(p, token)
		expect(t, status, out, 200, "")
		if task(out).Status != "succeeded" {
			t.Fatalf("large saved HTML failed recovery: %+v", task(out))
		}
		_, _, _, drafts := w.counts()
		if drafts != 1 {
			t.Fatal("restart duplicated draft")
		}
	}
}
