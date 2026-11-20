package wechatdraft_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

func TestHTTPRetryRollbackAndCleanupDoNotDoubleReleaseBytes(t *testing.T) {
	w := newWX(t)
	w.setMode("ip")
	entered, resume := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	release := func() { unblock.Do(func() { close(resume) }) }
	defer release()
	var enabled, blocked atomic.Bool
	c := config(t)
	c.Fault = func(op, path string) error {
		if enabled.Load() && op == "remove" && strings.Contains(path, ".staging") && blocked.CompareAndSwap(false, true) {
			close(entered)
			<-resume
		}
		return nil
	}
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", true)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	h.wait(p, token, "failed")
	p.ExpectedAttempt, p.RetryRequestID = 1, uuid.NewString()
	w.setMode("")
	enabled.Store(true)
	done := make(chan int, 1)
	go func() { status, _ := h.post(p, token, files, true); done <- status }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		release()
		t.Fatal("retry did not reach stage rollback")
	}
	h.manager.Cleanup()
	release()
	if status := <-done; status != 202 {
		t.Fatal("retry not accepted", status)
	}
	h.wait(p, token, "succeeded")
	var actual int64
	err := filepath.Walk(c.DataDir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			actual += info.Size()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	stats := h.manager.Stats()
	if stats["diskUsed"] != actual || stats["diskReserved"] != 0 {
		t.Fatalf("cleanup/rollback double-counted bytes: stats=%v actual=%d", stats, actual)
	}
}

func TestHTTPRestartRejectsContradictorySuccessEvidence(t *testing.T) {
	for _, damage := range []string{"queued-done", "running-done", "blank-media", "wrong-progress", "wrong-retry"} {
		t.Run(damage, func(t *testing.T) {
			w, c := newWX(t), config(t)
			h := startHarness(t, w, c)
			p, token, files := payload("wx0123456789abcdef", true)
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 202, "")
			h.wait(p, token, "succeeded")
			dir := taskDir(h, p)
			h.close()
			path := filepath.Join(dir, "status.json")
			b, _ := os.ReadFile(path)
			var st map[string]any
			if err := json.Unmarshal(b, &st); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "queued-done", "running-done":
				st["status"] = strings.TrimSuffix(damage, "-done")
				st["finishedAt"], st["expiresAt"], st["result"] = nil, nil, nil
			case "blank-media":
				st["result"].(map[string]any)["mediaId"] = " "
			case "wrong-progress":
				st["progress"].(map[string]any)["percent"] = 25
			case "wrong-retry":
				st["attempt"] = 2
			}
			b, _ = json.Marshal(st)
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			h = startHarness(t, w, c)
			s, out = h.get(p, token)
			expect(t, s, out, 200, "")
			if got := task(out); got.Status != "uncertain" || got.Phase != "recovery_unknown" || got.RetryAllowed {
				t.Fatalf("contradictory evidence trusted: %+v", got)
			}
			if got := task(out); got.Progress.Total != len(p.Images)+3 {
				t.Fatalf("recovery lost the immutable receipt's image count: %+v", got.Progress)
			}
			_, _, _, drafts := w.counts()
			if drafts != 1 {
				t.Fatal("corrupted evidence recreated draft")
			}
		})
	}
}

func TestHTTPCorruptRecoveryMarkerDoesNotPreventStartupOrExtendExpiry(t *testing.T) {
	w, c := newWX(t), config(t)
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", false)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	h.wait(p, token, "succeeded")
	dir := taskDir(h, p)
	h.close()
	if err := os.Remove(filepath.Join(dir, "status.json")); err != nil {
		t.Fatal(err)
	}
	h = startHarness(t, w, c)
	s, out = h.get(p, token)
	expect(t, s, out, 200, "")
	oldExpiry := *task(out).ExpiresAt
	h.close()
	if err := os.WriteFile(filepath.Join(dir, "recovery.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := newManager(c, w)
	if err != nil {
		t.Fatal("one corrupt isolation marker prevented service startup", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer m.Close(ctx)
	hash, _ := logic.TokenHash(token)
	got, e := m.Get(p.AppID, p.UUID, hash)
	if e != nil || got.Status != "uncertain" || got.ExpiresAt.After(oldExpiry) {
		t.Fatalf("isolation was not preserved: %+v %v", got, e)
	}
}
