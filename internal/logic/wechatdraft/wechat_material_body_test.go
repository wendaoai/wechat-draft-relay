package wechatdraft

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestMaterialUsesExactLengthAndReopensFileForRetry(t *testing.T) {
	for _, cover := range []bool{true, false} {
		t.Run(map[bool]string{true: "cover", false: "body"}[cover], func(t *testing.T) {
			data := bytes.Repeat([]byte("fixture-image"), 900000)
			path := filepath.Join(t.TempDir(), "cover.png")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.ContentLength <= int64(len(data)) || len(r.TransferEncoding) != 0 {
					t.Errorf("upload used unknown/chunked length: %d %v", r.ContentLength, r.TransferEncoding)
					w.WriteHeader(http.StatusLengthRequired)
					return
				}
				reader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				part, err := reader.NextPart()
				if err != nil || part.FormName() != "media" || part.FileName() != "cover.png" {
					t.Error("invalid multipart media")
					return
				}
				got, err := io.ReadAll(part)
				if err != nil || !bytes.Equal(got, data) {
					t.Error("truncated or altered image")
				}
				if _, err = reader.NextPart(); err != io.EOF {
					t.Error("invalid multipart closing boundary")
				}
				if calls.Load() == 1 {
					w.WriteHeader(503)
					return
				}
				if cover {
					io.WriteString(w, `{"media_id":"cover-receipt"}`)
				} else {
					io.WriteString(w, `{"url":"https://mmbiz.qpic.cn/body.png"}`)
				}
			}))
			defer server.Close()
			w := NewLocalWechat(server.URL)
			defer w.Close()
			value, err := w.Material(context.Background(), "fixture-token", path, cover)
			if err != nil || value == "" || calls.Load() != 2 {
				t.Fatalf("upload result=%q failure=%v calls=%d", value, err, calls.Load())
			}
		})
	}
}

func TestMissingMaterialDoesNotCallWechatOrExposePath(t *testing.T) {
	w := NewLocalWechat("http://127.0.0.1:1")
	defer w.Close()
	_, failure := w.Material(context.Background(), "fixture-token", filepath.Join(t.TempDir(), "private-image.png"), true)
	if failure == nil || failure.Fault.Code != "LOCAL_MEDIA_UNAVAILABLE" || failure.Temporary {
		t.Fatalf("local failure incorrectly retried as network error: %v", failure)
	}
}
