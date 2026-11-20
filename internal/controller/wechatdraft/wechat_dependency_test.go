package wechatdraft_test

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type wxMock struct {
	server                      *httptest.Server
	mu                          sync.Mutex
	mode                        string
	tokens, cover, body, drafts int
	delay                       time.Duration
	blockToken, blockDraft      chan struct{}
	contents                    []string
	active, maxConnections      atomic.Int64
}

func newWX(t *testing.T) *wxMock {
	w := &wxMock{}
	w.server = httptest.NewUnstartedServer(http.HandlerFunc(w.handle))
	w.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			n := w.active.Add(1)
			for {
				old := w.maxConnections.Load()
				if n <= old || w.maxConnections.CompareAndSwap(old, n) {
					break
				}
			}
		} else if state == http.StateClosed || state == http.StateHijacked {
			w.active.Add(-1)
		}
	}
	w.server.Start()
	t.Cleanup(w.server.Close)
	return w
}
func (w *wxMock) setMode(mode string) { w.mu.Lock(); w.mode = mode; w.mu.Unlock() }
func (w *wxMock) counts() (int, int, int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tokens, w.cover, w.body, w.drafts
}
func (w *wxMock) handle(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	mode, delay, tokenBlock, draftBlock := w.mode, w.delay, w.blockToken, w.blockDraft
	w.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	rw.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/cgi-bin/stable_token":
		w.mu.Lock()
		w.tokens++
		w.mu.Unlock()
		if tokenBlock != nil {
			select {
			case <-tokenBlock:
			case <-r.Context().Done():
				return
			}
		}
		if mode == "ip" {
			io.WriteString(rw, `{"errcode":40164,"errmsg":"`+testSecret+`"}`)
			return
		}
		if mode == "secret" {
			io.WriteString(rw, `{"errcode":40125}`)
			return
		}
		if mode == "permission" {
			io.WriteString(rw, `{"errcode":48001}`)
			return
		}
		w.mu.Lock()
		tokenCalls := w.tokens
		w.mu.Unlock()
		fmt.Fprintf(rw, `{"access_token":"wx-token-%d","expires_in":7200}`, tokenCalls)
	case "/cgi-bin/material/add_material", "/cgi-bin/media/uploadimg":
		if mode == "material" {
			io.WriteString(rw, `{"errcode":40007}`)
			return
		}
		if mode == "expired" && r.URL.Query().Get("access_token") == "wx-token-1" {
			io.WriteString(rw, `{"errcode":42001}`)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			rw.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, _, err := r.FormFile("media")
		if err != nil {
			rw.WriteHeader(400)
			return
		}
		_, _, err = image.Decode(f)
		f.Close()
		if err != nil {
			rw.WriteHeader(400)
			return
		}
		w.mu.Lock()
		if strings.Contains(r.URL.Path, "add_material") {
			w.cover++
			io.WriteString(rw, `{"media_id":"real-mock-cover"}`)
		} else {
			w.body++
			io.WriteString(rw, `{"url":"https://mmbiz.qpic.cn/test/image.png"}`)
		}
		w.mu.Unlock()
	case "/cgi-bin/draft/add":
		var input struct {
			Articles []struct {
				Content string `json:"content"`
				Thumb   string `json:"thumb_media_id"`
			}
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Articles) != 1 || input.Articles[0].Thumb != "real-mock-cover" {
			rw.WriteHeader(400)
			return
		}
		w.mu.Lock()
		w.drafts++
		number := w.drafts
		w.contents = append(w.contents, input.Articles[0].Content)
		w.mu.Unlock()
		if draftBlock != nil {
			select {
			case <-draftBlock:
			case <-r.Context().Done():
				return
			}
		}
		switch mode {
		case "lost":
			conn, _, _ := rw.(http.Hijacker).Hijack()
			conn.Close()
			return
		case "busy":
			io.WriteString(rw, `{"errcode":-1}`)
			return
		case "badjson":
			io.WriteString(rw, `{"media_id":`)
			return
		case "nomedia":
			io.WriteString(rw, `{}`)
			return
		case "5xx":
			rw.WriteHeader(503)
			return
		case "draft-invalid":
			io.WriteString(rw, `{"errcode":45003}`)
			return
		}
		fmt.Fprintf(rw, `{"media_id":"mock-draft-%d"}`, number)
	default:
		rw.WriteHeader(404)
	}
}
