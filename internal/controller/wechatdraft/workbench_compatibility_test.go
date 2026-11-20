package wechatdraft_test

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func TestHTTPWorkbenchLayoutWithoutExtraWhitelist(t *testing.T) {
	w := newWX(t)
	h := startHarness(t, w, config(t))
	p, token, files := payload("wx0123456789abcdef", true)
	p.Article.ContentHTML = `<section class="article" id="draft" style="--space: 12px; box-sizing: border-box; width: calc(100% - 24px); padding: var(--space); display: grid; gap: 12px; text-indent: 2em; max-width: 20000px !important"><!--图注保留--><figure align="center"><img src="cw-image://body" width="100%"><figcaption style="color: papayawhip; text-wrap: pretty">正文图注</figcaption></figure></section>`
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	h.wait(p, token, "succeeded")
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.contents) != 1 {
		t.Fatal("expected one draft")
	}
	for _, preserved := range []string{"box-sizing", "calc(", "var(", "20000px !important", "<figure", "<figcaption", "正文图注", `class="article"`, "<!--图注保留-->", "https://mmbiz.qpic.cn/test/image.png"} {
		if !strings.Contains(w.contents[0], preserved) {
			t.Fatalf("layout lost %q", preserved)
		}
	}
}

func TestHTTPImageDimensionsMatchWorkbench(t *testing.T) {
	w := newWX(t)
	h := startHarness(t, w, config(t))
	for _, tc := range []struct {
		name          string
		width, height int
		status        int
	}{
		{"over_old_axis_and_pixels", 12000, 1800, 202},
		{"over_workbench_pixels", 6000, 4001, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, tc.width, tc.height))); err != nil {
				t.Fatal(err)
			}
			p, token, files := payload("wx0123456789abcdef", false)
			files["file_cover"] = b.Bytes()
			p.Images[0].SHA256 = hash(b.Bytes())
			s, out := h.post(p, token, files, false)
			code := ""
			if tc.status == 422 {
				code = "IMAGE_INVALID"
			}
			expect(t, s, out, tc.status, code)
			if tc.status == 202 {
				h.wait(p, token, "succeeded")
			}
		})
	}
	_, covers, _, drafts := w.counts()
	if covers != 1 || drafts != 1 {
		t.Fatal("invalid dimensions reached WeChat or valid image was lost")
	}
}

func TestHTTPMoreThanFiftyImagesAccepted(t *testing.T) {
	w := newWX(t)
	h := startHarness(t, w, config(t))
	p, token, files := payload("wx0123456789abcdef", false)
	for i := 0; i < 50; i++ {
		id := strings.Repeat("x", i+1)
		p.Images = append(p.Images, v1.Image{ID: id, Role: "body", FileField: "file_" + id, SHA256: hash(tinyImage())})
		p.Article.ContentHTML += `<img src="cw-image://` + id + `">`
		files["file_"+id] = tinyImage()
	}
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	h.wait(p, token, "succeeded")
	_, covers, bodies, drafts := w.counts()
	if covers != 1 || bodies != 50 || drafts != 1 {
		t.Fatal("declared images did not reach WeChat")
	}
}
