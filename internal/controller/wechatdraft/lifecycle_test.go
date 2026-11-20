package wechatdraft_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHTTPRetryLateReplayAndOldCancel(t *testing.T) {
	w := newWX(t)
	w.setMode("ip")
	h := startHarness(t, w, config(t))
	p, token, files := payload("wx0123456789abcdef", true)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	st := h.wait(p, token, "failed")
	if st.Error.Code != "WECHAT_IP_NOT_ALLOWED" || !st.RetryAllowed {
		t.Fatalf("failure %+v", st)
	}
	q := p
	q.ExpectedAttempt = 1
	q.RetryRequestID = uuid.NewString()
	s, out = h.post(q, token, files, true)
	expect(t, s, out, 202, "")
	st = h.wait(p, token, "failed")
	if st.Attempt != 2 {
		t.Fatal(st.Attempt)
	}
	s, out = h.post(q, token, nil, true)
	expect(t, s, out, 200, "")
	if task(out).Attempt != 2 {
		t.Fatal("late retry started next round")
	}
	q.ExpectedAttempt = 2
	s, out = h.post(q, token, nil, true)
	expect(t, s, out, 409, "RETRY_OPERATION_CONFLICT")
	q.RetryRequestID = uuid.NewString()
	q.ExpectedAttempt = 1
	s, out = h.post(q, token, files, true)
	expect(t, s, out, 409, "ATTEMPT_CONFLICT")
	path := "/api/v1/wechat-drafts/" + p.AppID + "/" + p.UUID + "/cancel"
	s, out, _ = h.request("POST", path, token, p.AppID, p.UUID, "application/json", strings.NewReader(`{"expectedAttempt":1}`))
	expect(t, s, out, 409, "ATTEMPT_CONFLICT")
	w.setMode("")
	q.ExpectedAttempt = 2
	s, out = h.post(q, token, files, true)
	expect(t, s, out, 202, "")
	st = h.wait(p, token, "succeeded")
	if st.Attempt != 3 {
		t.Fatal(st.Attempt)
	}
	_, _, _, drafts := w.counts()
	if drafts != 1 {
		t.Fatal("retry created duplicate drafts")
	}
}

func TestHTTPResponseLossAndQueueFullRecovery(t *testing.T) {
	w := newWX(t)
	block := make(chan struct{})
	w.blockToken = block
	c := config(t)
	c.Workers = 1
	c.QueueSize = 1
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", false)
	b, ct := multipartBody(p, files)
	req, _ := http.NewRequest("POST", h.url+"/api/v1/wechat-drafts", bytes.NewReader(b))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("X-Draft-Token", token)
	req.Header.Set("X-Wechat-AppId", p.AppID)
	req.Header.Set("X-Draft-UUID", p.UUID)
	res, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Drop the creation response without consuming the task result.
	res.Body.Close()
	deadline := time.Now().Add(time.Second)
	for {
		_, out := h.get(p, token)
		if task(out).Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first worker never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	q, token2, files2 := payload("wx1111111111111111", false)
	s, out := h.post(q, token2, files2, false)
	expect(t, s, out, 202, "")
	r, token3, files3 := payload("wx2222222222222222", false)
	s, out = h.post(r, token3, files3, false)
	expect(t, s, out, 429, "QUEUE_FULL")
	s, out = h.post(p, token, nil, false)
	expect(t, s, out, 200, "")
	if task(out).Status != "running" {
		t.Fatal("existing task not restored")
	}
	s, out = h.get(p, token)
	expect(t, s, out, 200, "")
	p.Article.Title = "不同清单"
	s, out = h.post(p, token, nil, false)
	expect(t, s, out, 409, "IDEMPOTENCY_CONFLICT")
	p.Article.Title = "验收文章"
	close(block)
	h.wait(p, token, "succeeded")
	h.wait(q, token2, "succeeded")
	_, _, _, drafts := w.counts()
	if drafts != 2 {
		t.Fatal(drafts)
	}
}

func TestHTTPQueueTimeoutCancellationAndQuotaSeparation(t *testing.T) {
	w := newWX(t)
	block := make(chan struct{})
	defer close(block)
	w.blockToken = block
	c := config(t)
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	c.Now = func() time.Time { return time.Unix(0, now.Load()) }
	c.Workers = 1
	c.QueueSize = 2
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", false)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, out = h.get(p, token)
		if task(out).Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not running")
		}
		time.Sleep(10 * time.Millisecond)
	}
	q, token2, files2 := payload("wx1111111111111111", false)
	s, out = h.post(q, token2, files2, false)
	expect(t, s, out, 202, "")
	now.Add(int64(61 * time.Second))
	time.Sleep(1100 * time.Millisecond)
	s, out = h.get(q, token2)
	expect(t, s, out, 200, "")
	if task(out).Error == nil || task(out).Error.Code != "QUEUE_TIMEOUT" {
		t.Fatalf("timeout %+v", task(out))
	}
	// Saturate per-task query quota. Cancel has separate resources and counters.
	for i := 0; i < 61; i++ {
		s, out = h.get(p, token)
	}
	expect(t, s, out, 429, "QUERY_RATE_LIMIT")
	s, out, _ = h.request("POST", "/api/v1/wechat-drafts/"+p.AppID+"/"+p.UUID+"/cancel", token, p.AppID, p.UUID, "application/json", strings.NewReader(`{"expectedAttempt":1}`))
	expect(t, s, out, 200, "")
	// Allow the request context to terminate instead of unblocking the remote call.
	deadline = time.Now().Add(3 * time.Second)
	for {
		s, out = h.post(p, token, nil, false)
		expect(t, s, out, 200, "")
		if task(out).Status == "cancelled" && h.manager.Stats()["active"] == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancel %+v", task(out))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if h.manager.Stats()["queue"] != 0 || h.manager.Stats()["active"] != 0 {
		t.Fatal("expired/cancelled quotas were not released")
	}
}

func TestHTTPWechatFailuresAndUncertainty(t *testing.T) {
	for _, mode := range []string{"secret", "permission", "material", "expired", "lost", "busy", "badjson", "nomedia", "5xx", "draft-invalid"} {
		t.Run(mode, func(t *testing.T) {
			w := newWX(t)
			w.setMode(mode)
			h := startHarness(t, w, config(t))
			p, token, files := payload("wx0123456789abcdef", true)
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 202, "")
			wanted := "failed"
			if mode == "expired" {
				wanted = "succeeded"
			}
			if mode == "lost" || mode == "busy" || mode == "badjson" || mode == "nomedia" || mode == "5xx" {
				wanted = "uncertain"
			}
			st := h.wait(p, token, wanted)
			b, _ := json.Marshal(st)
			if bytes.Contains(b, []byte(testSecret)) || bytes.Contains(b, []byte("wx-token-")) {
				t.Fatal("credentials leaked")
			}
			if wanted == "uncertain" {
				if st.RetryAllowed || st.Result != nil {
					t.Fatal("unknown result treated as successful/retryable")
				}
				p.ExpectedAttempt = 1
				p.RetryRequestID = uuid.NewString()
				s, out = h.post(p, token, files, true)
				expect(t, s, out, 409, "RETRY_NOT_ALLOWED")
				_, _, _, drafts := w.counts()
				if drafts != 1 {
					t.Fatalf("uncertain draft resent: %d", drafts)
				}
			}
		})
	}
}

func TestHTTPSubmittingRejectsCancellation(t *testing.T) {
	w := newWX(t)
	block := make(chan struct{})
	w.blockDraft = block
	h := startHarness(t, w, config(t))
	p, token, files := payload("wx0123456789abcdef", false)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, _, n := w.counts()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("draft not received")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, out, _ = h.request("POST", "/api/v1/wechat-drafts/"+p.AppID+"/"+p.UUID+"/cancel", token, p.AppID, p.UUID, "application/json", strings.NewReader(`{"expectedAttempt":1}`))
	expect(t, s, out, 409, "CANCEL_TOO_LATE")
	close(block)
	h.wait(p, token, "succeeded")
}
