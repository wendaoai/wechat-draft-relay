package wechatdraft_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

func TestHTTPCloseDeadlineDuringQueuedStateFlush(t *testing.T) {
	w, c := newWX(t), config(t)
	c.Workers = 1
	blockWX, blockDisk := make(chan struct{}), make(chan struct{})
	var wxOnce, diskOnce sync.Once
	releaseWX := func() { wxOnce.Do(func() { close(blockWX) }) }
	releaseDisk := func() { diskOnce.Do(func() { close(blockDisk) }) }
	defer releaseWX()
	defer releaseDisk()
	w.blockToken = blockWX
	q, token, files := payload("wx2222222222222222", false)
	var armed, blocked atomic.Bool
	entered := make(chan struct{})
	c.Fault = func(op, path string) error {
		if armed.Load() && op == "sync" && path == filepath.Join(c.DataDir, q.AppID, q.UUID, "status.json") && blocked.CompareAndSwap(false, true) {
			close(entered)
			<-blockDisk
		}
		return nil
	}
	h := startHarness(t, w, c)
	p, tok, fs := payload("wx1111111111111111", false)
	s, out := h.post(p, tok, fs, false)
	expect(t, s, out, 202, "")
	deadline := time.Now().Add(2 * time.Second)
	for {
		tokens, _, _, _ := w.counts()
		if tokens == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not reach local WeChat")
		}
		time.Sleep(time.Millisecond)
	}
	s, out = h.post(q, token, files, false)
	expect(t, s, out, 202, "")
	armed.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.manager.Close(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not reach queued state flush")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unexpected close result: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		releaseDisk()
		<-done
		t.Fatal("Close ignored deadline while flushing queued state")
	}
	// An unfinished disk operation must retain ownership of the directory.
	if duplicate, err := newManager(c, w); err == nil {
		duplicate.Close(context.Background())
		t.Fatal("instance lock released before shutdown completed")
	}
	releaseDisk()
	releaseWX()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if err := h.manager.Close(ctx2); err != nil {
		t.Fatal("repeated Close did not join completed shutdown", err)
	}
	h.server.Shutdown()
	h.client.CloseIdleConnections()
	h.manager = nil
	recovered, err := newManager(c, w)
	if err != nil {
		t.Fatal("directory lock leaked after shutdown", err)
	}
	defer recovered.Close(ctx2)
	tokenHash, _ := logic.TokenHash(token)
	state, e := recovered.Get(q.AppID, q.UUID, tokenHash)
	if e != nil || state.Status != "failed" || state.Error == nil || state.Error.Code != "SERVER_RESTARTED" {
		t.Fatalf("queued state not durably finalized: %+v %+v", state, e)
	}
	_, _, _, drafts := w.counts()
	if drafts != 0 {
		t.Fatal("shutdown or recovery unexpectedly created a draft")
	}
}
