package wechatdraft_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func TestHTTPSameIPNormalPollingAndSeparateTaskQuota(t *testing.T) {
	c := config(t)
	c.QueryRate = 1000
	h := startHarness(t, newWX(t), c)
	var tasks []v1.Payload
	var tokens []string
	for _, app := range []string{"wx0123456789abcdef", "wx1111111111111111", "wx2222222222222222"} {
		p, token, files := payload(app, false)
		s, out := h.post(p, token, files, false)
		expect(t, s, out, 202, "")
		h.wait(p, token, "succeeded")
		tasks = append(tasks, p)
		tokens = append(tokens, token)
	}
	// Thirty polls per client represent one minute at the specified 2s cadence.
	// Compressing time creates a stricter burst without advancing limiter TTLs.
	var wg sync.WaitGroup
	for i, p := range tasks {
		wg.Add(1)
		go func(p v1.Payload, token string) {
			defer wg.Done()
			for n := 0; n < 30; n++ {
				s, out := h.get(p, token)
				expect(t, s, out, 200, "")
			}
		}(p, tokens[i])
	}
	wg.Wait()
	for n := 0; n < 61; n++ {
		h.get(tasks[0], tokens[0])
	}
	s, out := h.get(tasks[0], tokens[0])
	expect(t, s, out, 429, "QUERY_RATE_LIMIT")
	s, out = h.get(tasks[1], tokens[1])
	expect(t, s, out, 200, "")
}

func TestHTTPUploadsDoNotConsumeQueryOrCancelSlots(t *testing.T) {
	w := newWX(t)
	block := make(chan struct{})
	w.blockToken = block
	h := startHarness(t, w, config(t))
	p, token, files := payload("wx0123456789abcdef", false)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	deadline := time.Now().Add(time.Second)
	for {
		n, _, _, _ := w.counts()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first worker not running")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var writers []*io.PipeWriter
	var cancels []context.CancelFunc
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		q, tok, _ := payload([]string{"wx1111111111111111", "wx2222222222222222", "wx3333333333333333", "wx4444444444444444"}[i], false)
		reader, writer := io.Pipe()
		writers = append(writers, writer)
		mw := multipart.NewWriter(writer)
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		req, _ := http.NewRequestWithContext(ctx, "POST", h.url+"/api/v1/wechat-drafts", reader)
		req.Header.Set("Authorization", "Bearer "+testKey)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-Draft-Token", tok)
		req.Header.Set("X-Wechat-AppId", q.AppID)
		req.Header.Set("X-Draft-UUID", q.UUID)
		wg.Add(2)
		go func() {
			defer wg.Done()
			res, _ := h.client.Do(req)
			if res != nil {
				res.Body.Close()
			}
		}()
		go func() {
			defer wg.Done()
			part, _ := mw.CreateFormField("payload")
			json.NewEncoder(part).Encode(q)
			mw.CreateFormFile("file_cover", "cover.png")
		}()
	}
	deadline = time.Now().Add(2 * time.Second)
	for h.manager.Stats()["uploads"] != 4 {
		if time.Now().After(deadline) {
			t.Fatal("uploads not saturated", h.manager.Stats())
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, out = h.get(p, token)
	expect(t, s, out, 200, "")
	s, out, _ = h.request("POST", "/api/v1/wechat-drafts/"+p.AppID+"/"+p.UUID+"/cancel", token, p.AppID, p.UUID, "application/json", strings.NewReader(`{"expectedAttempt":1}`))
	expect(t, s, out, 200, "")
	for _, cancel := range cancels {
		cancel()
	}
	for _, writer := range writers {
		writer.Close()
	}
	wg.Wait()
	close(block)
	deadline = time.Now().Add(2 * time.Second)
	for h.manager.Stats()["uploads"] != 0 {
		if time.Now().After(deadline) {
			t.Fatal("upload cancellation leaked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.manager.Stats()["diskReserved"] != 0 {
		t.Fatal("aborted upload leaked bytes")
	}
}

func TestHTTPBoundedControlAndMultipartOrdering(t *testing.T) {
	h := startHarness(t, newWX(t), config(t))
	p, token, _ := payload("wx0123456789abcdef", false)
	oversized := bytes.Repeat([]byte("x"), 1<<20+1)
	var body bytes.Buffer
	ct := multipartWriter(&body, oversized)
	s, out, _ := h.request("POST", "/api/v1/wechat-drafts", token, p.AppID, p.UUID, ct, &body)
	// Large control parts are now accepted for decoding; malformed JSON still fails.
	expect(t, s, out, 422, "VALIDATION_FAILED")
	body.Reset()
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file_cover", "cover.png")
	part.Write(tinyImage())
	part, _ = mw.CreateFormField("payload")
	json.NewEncoder(part).Encode(p)
	mw.Close()
	s, out, _ = h.request("POST", "/api/v1/wechat-drafts", token, p.AppID, p.UUID, mw.FormDataContentType(), &body)
	expect(t, s, out, 422, "VALIDATION_FAILED")
}
