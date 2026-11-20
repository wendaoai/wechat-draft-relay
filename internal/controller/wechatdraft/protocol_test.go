package wechatdraft_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestHTTPNormalSnapshotAndIdempotency(t *testing.T) {
	w := newWX(t)
	h := startHarness(t, w, config(t))
	p, token, files := payload("wx0123456789abcdef", true)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	if task(out).Progress.Completed != 1 {
		t.Fatal("acceptance fabricated execution progress")
	}
	final := h.wait(p, token, "succeeded")
	if final.Result == nil || final.Result.MediaID != "mock-draft-1" || final.Progress.Percent != 100 || final.Progress.Total != 5 {
		t.Fatalf("invalid result %+v", final)
	}
	if !strings.HasSuffix(final.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"), "+08:00") {
		t.Fatal("time zone")
	}
	for _, name := range []string{"article.json", "original.html", "wechat.html", "status.json", "receipt.json", "images/cover.png", "images/body.png"} {
		if _, err := os.Stat(filepath.Join(taskDir(h, p), name)); err != nil {
			t.Fatal(name, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(taskDir(h, p), "wechat.html"))
	if strings.Contains(string(b), "cw-image://") || !strings.Contains(string(b), "https://mmbiz.qpic.cn/test/image.png") {
		t.Fatal("images were not replaced")
	}
	// The replay sends only its declaration: files are intentionally absent.
	p.AppSecret = ""
	s, out = h.post(p, token, nil, false)
	expect(t, s, out, 200, "")
	if task(out).Result.MediaID != final.Result.MediaID {
		t.Fatal("receipt changed")
	}
	_, cover, body, drafts := w.counts()
	if cover != 1 || body != 1 || drafts != 1 {
		t.Fatalf("duplicate writes %d/%d/%d", cover, body, drafts)
	}
	p.Article.Title = "内容变化"
	s, out = h.post(p, token, files, false)
	expect(t, s, out, 409, "IDEMPOTENCY_CONFLICT")
	err := filepath.WalkDir(taskDir(h, p), func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, secret := range []string{testSecret, testKey, token} {
				if bytes.Contains(b, []byte(secret)) {
					t.Errorf("credential in %s", e.Name())
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHTTPAuthenticationAndIndependentValidation(t *testing.T) {
	h := startHarness(t, newWX(t), config(t))
	p, token, files := payload("wx0123456789abcdef", false)
	res, err := http.Get(h.url + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(b) != `{"status":"ok"}` {
		t.Fatalf("ready %s", b)
	}
	req, _ := http.NewRequest("POST", h.url+"/api/v1/wechat-drafts", nil)
	res, err = h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	h.wait(p, token, "succeeded")
	_, wrong, _ := payload("wx1111111111111111", false)
	s, out = h.get(p, wrong)
	expect(t, s, out, 404, "TASK_NOT_FOUND")
	for _, id := range []string{uuid.NewString(), "../escape", "not-a-uuid", "01a12f04-b4c0-7b47-0e07-5c1de520f084"} {
		q := p
		q.UUID = id
		s, out = h.post(q, token, files, false)
		expect(t, s, out, 422, "VALIDATION_FAILED")
	}
	q, token2, files2 := payload("wx1111111111111111", false)
	q.AppID = p.AppID
	b2, ct := multipartBody(q, files2)
	s, out, _ = h.request("POST", "/api/v1/wechat-drafts", token2, "wx2222222222222222", q.UUID, ct, bytes.NewReader(b2))
	expect(t, s, out, 422, "VALIDATION_FAILED")
	// JSON decoder errors must never expose raw input (including secrets).
	bad := []byte(`{"schemaVersion":1,"appsecret":"` + testSecret + `","unknownSecretField":"` + testSecret + `"}`)
	var body bytes.Buffer
	writer := multipartWriter(&body, bad)
	s, out, _ = h.request("POST", "/api/v1/wechat-drafts", token2, q.AppID, q.UUID, writer, bytes.NewReader(body.Bytes()))
	expect(t, s, out, 422, "VALIDATION_FAILED")
	encoded, _ := json.Marshal(out)
	if bytes.Contains(encoded, []byte(testSecret)) {
		t.Fatal("credential leaked")
	}
}

func TestHTTPConcurrentSameKeyAndFalseHash(t *testing.T) {
	h := startHarness(t, newWX(t), config(t))
	p, token, files := payload("wx0123456789abcdef", true)
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, out := h.post(p, token, files, false)
			if s != 202 && s != 200 && (out.Error == nil || out.Error.Code != "CREATE_IN_PROGRESS") {
				t.Errorf("concurrent %d %+v", s, out)
			}
			results <- s
		}()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for s := range results {
		if s == 202 {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d", accepted)
	}
	h.wait(p, token, "succeeded")
	_, _, _, drafts := h.wx.counts()
	if drafts != 1 {
		t.Fatal(drafts)
	}
	q, token2, files2 := payload("wx1111111111111111", false)
	q.Images[0].SHA256 = strings.Repeat("a", 64)
	s, out := h.post(q, token2, files2, false)
	expect(t, s, out, 422, "IMAGE_HASH_MISMATCH")
	if _, err := os.Stat(taskDir(h, q)); !os.IsNotExist(err) {
		t.Fatal("invalid task accepted")
	}
	entries, _ := os.ReadDir(filepath.Join(h.config.DataDir, ".staging"))
	if len(entries) != 0 {
		t.Fatal("staging leaked")
	}
	if h.manager.Stats()["diskReserved"] != 0 {
		t.Fatal("reservation leaked")
	}
}

func TestHTTPImageAndHTMLBoundaries(t *testing.T) {
	h := startHarness(t, newWX(t), config(t))
	cases := []struct{ name, html, code string }{
		{"external", `<p>x<img src="https://example.com/image.png"></p>`, "IMAGE_REFERENCE_INVALID"},
		{"data", `<img src="data:image/png;base64,eA==">`, "IMAGE_REFERENCE_INVALID"},
		{"script", `<script>alert(1)</script>`, "VALIDATION_FAILED"},
		{"event", `<p onclick="x()">x</p>`, "VALIDATION_FAILED"},
		{"css", `<p style="background-color:url(http://localhost)">x</p>`, "VALIDATION_FAILED"},
		{"cssescape", `<p style="color:r\65 d">x</p>`, "VALIDATION_FAILED"},
		{"svg", `<svg></svg>`, "VALIDATION_FAILED"},
		{"srcset", `<img src="cw-image://body" srcset="x">`, "VALIDATION_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, token, files := payload("wx0123456789abcdef", false)
			p.Article.ContentHTML = tc.html
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 422, tc.code)
		})
	}
	p, token, files := payload("wx1111111111111111", false)
	s, out := h.post(p, token, nil, false)
	expect(t, s, out, 422, "IMAGE_REFERENCE_INVALID")
	files["file_cover"] = []byte("not an image")
	p.Images[0].SHA256 = hash(files["file_cover"])
	s, out = h.post(p, token, files, false)
	expect(t, s, out, 422, "IMAGE_INVALID")
	_, _, _, drafts := h.wx.counts()
	if drafts != 0 {
		t.Fatal("invalid data called draft/add")
	}
}
