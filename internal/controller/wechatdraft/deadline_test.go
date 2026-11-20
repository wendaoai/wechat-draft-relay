package wechatdraft_test

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"
)

func TestHTTPAdmittedUploadExtendsControlDeadline(t *testing.T) {
	h := startHarness(t, newWX(t), config(t))
	p, token, files := payload("wx0123456789abcdef", false)
	reader, writer := io.Pipe()
	mw := multipart.NewWriter(writer)
	req, _ := http.NewRequest("POST", h.url+"/api/v1/wechat-drafts", reader)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("X-Draft-Token", token)
	req.Header.Set("X-Wechat-AppId", p.AppID)
	req.Header.Set("X-Draft-UUID", p.UUID)
	result := make(chan int, 1)
	go func() {
		res, err := h.client.Do(req)
		if err != nil {
			result <- 0
			return
		}
		res.Body.Close()
		result <- res.StatusCode
	}()
	part, err := mw.CreateFormField("payload")
	if err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(part).Encode(p)
	file, err := mw.CreateFormFile("file_cover", "cover.png")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(6 * time.Second)
	if _, err = file.Write(files["file_cover"]); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	writer.Close()
	if status := <-result; status != 202 {
		t.Fatalf("admitted body retained 5s control deadline: %d", status)
	}
	h.wait(p, token, "succeeded")
}

func TestHTTPExecutionDeadlineBeforeAndAfterSubmission(t *testing.T) {
	for _, stage := range []string{"token", "draft"} {
		t.Run(stage, func(t *testing.T) {
			w := newWX(t)
			block := make(chan struct{})
			if stage == "token" {
				w.blockToken = block
			} else {
				w.blockDraft = block
			}
			c := config(t)
			c.TaskTimeout = time.Second
			h := startHarness(t, w, c)
			p, token, files := payload("wx0123456789abcdef", true)
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 202, "")
			want := "failed"
			if stage == "draft" {
				want = "uncertain"
			}
			st := h.wait(p, token, want)
			close(block)
			if stage == "draft" && st.RetryAllowed {
				t.Fatal("timed-out submission retryable")
			}
			_, _, _, drafts := w.counts()
			if stage == "draft" && drafts != 1 {
				t.Fatalf("draft submissions = %d, want 1", drafts)
			}
		})
	}
}
