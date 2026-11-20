package wechatdraft_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPTerminalCleanupExpiryReplayAndFailedDeletion(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	var deleteFails atomic.Bool
	c := config(t)
	c.Now = func() time.Time { return time.Unix(0, now.Load()) }
	c.Fault = func(op, path string) error {
		if op == "remove" && deleteFails.Load() {
			return errors.New("delete failed")
		}
		return nil
	}
	w := newWX(t)
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", true)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	final := h.wait(p, token, "succeeded")
	dir := taskDir(h, p)
	now.Store(final.FinishedAt.Add(23*time.Hour + 59*time.Minute).UnixNano())
	h.manager.Cleanup()
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("deleted before 24 hours")
	}
	// Existing valid tasks are recovered before checking the v7 creation window.
	s, out = h.post(p, token, nil, false)
	expect(t, s, out, 200, "")
	now.Store(final.FinishedAt.Add(24 * time.Hour).UnixNano())
	s, out = h.get(p, token)
	expect(t, s, out, 404, "TASK_NOT_FOUND")
	s, out = h.post(p, token, nil, false)
	expect(t, s, out, 409, "REQUEST_EXPIRED")
	before := h.manager.Stats()["diskUsed"]
	deleteFails.Store(true)
	h.manager.Cleanup()
	gc := filepath.Join(c.DataDir, p.AppID, ".gc-"+p.UUID)
	if _, err := os.Stat(gc); err != nil {
		t.Fatal("failed cleanup did not retain gc")
	}
	if h.manager.Stats()["diskUsed"] != before {
		t.Fatal("released budget before deletion")
	}
	deleteFails.Store(false)
	h.manager.Cleanup()
	if _, err := os.Stat(gc); !os.IsNotExist(err) {
		t.Fatal("gc not retried")
	}
	if h.manager.Stats()["diskUsed"] != 0 {
		t.Fatal("budget not released")
	}
	s, out = h.post(p, token, files, false)
	expect(t, s, out, 409, "REQUEST_EXPIRED")
	_, _, _, drafts := w.counts()
	if drafts != 1 {
		t.Fatal("expired UUID created another draft")
	}
	h.close()
	h = startHarness(t, w, c)
	s, out = h.post(p, token, files, false)
	expect(t, s, out, 409, "REQUEST_EXPIRED")
}

func TestHTTPStartupCatchupAndActiveTaskProtection(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	c := config(t)
	c.Now = func() time.Time { return time.Unix(0, now.Load()) }
	w := newWX(t)
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", true)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	final := h.wait(p, token, "succeeded")
	q, token2, files2 := payload("wx1111111111111111", false)
	block := make(chan struct{})
	w.mu.Lock()
	w.blockToken = block
	w.mu.Unlock()
	s, out = h.post(q, token2, files2, false)
	expect(t, s, out, 202, "")
	deadline := time.Now().Add(2 * time.Second)
	for {
		tokens, _, _, _ := w.counts()
		if tokens >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second task did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	now.Store(final.FinishedAt.Add(25 * time.Hour).UnixNano())
	h.manager.Cleanup()
	if _, err := os.Stat(taskDir(h, q)); err != nil {
		t.Fatal("active task deleted")
	}
	close(block)
	h.wait(q, token2, "succeeded")
	if _, err := os.Stat(taskDir(h, p)); !os.IsNotExist(err) {
		t.Fatal("terminal task not removed")
	}
	h.close()
	now.Add(int64(25 * time.Hour))
	h = startHarness(t, w, c)
	if _, err := os.Stat(taskDir(h, q)); !os.IsNotExist(err) {
		t.Fatal("startup did not catch up expired task")
	}
	_, _, _, drafts := w.counts()
	if drafts != 2 {
		t.Fatal("cleanup contacted Wechat")
	}
}

func TestHTTPOrphanIsolationCleanupAndInstanceLock(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	c := config(t)
	c.Now = func() time.Time { return time.Unix(0, now.Load()) }
	p, token, files := payload("wx0123456789abcdef", false)
	dir := filepath.Join(c.DataDir, p.AppID, p.UUID)
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "orphan.bin"), []byte("orphan"), 0600)
	w := newWX(t)
	h := startHarness(t, w, c)
	s, out := h.get(p, token)
	expect(t, s, out, 404, "TASK_NOT_FOUND")
	s, out = h.post(p, token, files, false)
	expect(t, s, out, 404, "TASK_NOT_FOUND")
	if _, err := os.Stat(filepath.Join(dir, "recovery.json")); err != nil {
		t.Fatal("orphan not isolated")
	}
	if _, err := newManager(c, w); err == nil {
		t.Fatal("second instance acquired same directory")
	}
	now.Add(int64(24 * time.Hour))
	h.manager.Cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("orphan retained forever")
	}
}
