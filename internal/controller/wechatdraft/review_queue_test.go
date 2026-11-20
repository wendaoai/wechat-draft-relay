package wechatdraft_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPBlockedCleanupDoesNotDelayQueueTimeout(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	w, c := newWX(t), config(t)
	c.Workers = 1
	c.Now = func() time.Time { return time.Unix(0, now.Load()) }
	c.CleanupInterval = 100 * time.Millisecond
	entered, unblockGC, unblockWX := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var gateOnce, wxOnce sync.Once
	releaseGC := func() { gateOnce.Do(func() { close(unblockGC) }) }
	releaseWX := func() { wxOnce.Do(func() { close(unblockWX) }) }
	defer releaseGC()
	defer releaseWX()
	var blocking, blocked atomic.Bool
	c.Fault = func(op, path string) error {
		if blocking.Load() && op == "remove" && strings.Contains(path, ".gc-") && blocked.CompareAndSwap(false, true) {
			close(entered)
			<-unblockGC
			return errors.New("bounded test deletion failure")
		}
		return nil
	}
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", false)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	st := h.wait(p, token, "succeeded")
	blocking.Store(true)
	now.Store(st.FinishedAt.Add(24 * time.Hour).UnixNano())
	w.mu.Lock()
	w.blockToken = unblockWX
	w.mu.Unlock()
	p, token, files = payload("wx1111111111111111", false)
	p.UUID = uuidAt(c.Now())
	s, out = h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	deadline := time.Now().Add(time.Second)
	for h.manager.Stats()["queue"] != 0 {
		if time.Now().After(deadline) {
			t.Fatal("worker did not start")
		}
		time.Sleep(time.Millisecond)
	}
	q, tok, fs := payload("wx2222222222222222", false)
	q.UUID = uuidAt(c.Now())
	s, out = h.post(q, tok, fs, false)
	expect(t, s, out, 202, "")
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not reach blocked filesystem dependency")
	}
	now.Add(int64(61 * time.Second))
	deadline = time.Now().Add(2200 * time.Millisecond)
	for {
		s, out = h.get(q, tok)
		expect(t, s, out, 200, "")
		got := task(out)
		if got.Status == "failed" && got.Error != nil && got.Error.Code == "QUEUE_TIMEOUT" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("filesystem cleanup stalled independent queue deadline", got.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
