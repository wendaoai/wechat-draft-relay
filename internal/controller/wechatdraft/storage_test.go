package wechatdraft_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

func TestHTTPWriteFaultsNeverAcceptIncompleteSnapshot(t *testing.T) {
	for _, op := range []string{"imagewrite", "write", "sync", "replace", "commit", "commitsync"} {
		t.Run(op, func(t *testing.T) {
			var enabled atomic.Bool
			enabled.Store(true)
			c := config(t)
			c.Fault = func(operation, path string) error {
				if enabled.Load() && operation == op {
					return errors.New("injected ENOSPC")
				}
				return nil
			}
			h := startHarness(t, newWX(t), c)
			p, token, files := payload("wx0123456789abcdef", true)
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 503, "STORAGE_UNAVAILABLE")
			_, _, _, drafts := h.wx.counts()
			if drafts != 0 {
				t.Fatal("incomplete acceptance executed")
			}
			if h.manager.Stats()["diskReserved"] != 0 {
				t.Fatal("reservation leaked")
			}
			enabled.Store(false)
			h.close()
			h = startHarness(t, h.wx, c)
			// A commit that became visible is preserved as its original task.
			s, out = h.get(p, token)
			if op == "commitsync" {
				expect(t, s, out, 200, "")
				if task(out).Status != "failed" {
					t.Fatal("commit failure silently queued")
				}
			} else {
				expect(t, s, out, 404, "TASK_NOT_FOUND")
			}
			staging, _ := os.ReadDir(filepath.Join(c.DataDir, ".staging"))
			if len(staging) != 0 {
				t.Fatal("restart did not clean staging")
			}
		})
	}
}

func TestHTTPWechatSuccessThenLocalReceiptFailure(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "finite", true: "persistent"}[persistent], func(t *testing.T) {
			w := newWX(t)
			var failures atomic.Int64
			c := config(t)
			c.Fault = func(op, path string) error {
				if op == "write" && strings.HasSuffix(path, "status.json") {
					_, _, _, drafts := w.counts()
					if drafts > 0 && (persistent || failures.Add(1) == 1) {
						return errors.New("receipt ENOSPC")
					}
				}
				return nil
			}
			h := startHarness(t, w, c)
			p, token, files := payload("wx0123456789abcdef", true)
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 202, "")
			wanted := "succeeded"
			if persistent {
				wanted = "uncertain"
			}
			st := h.wait(p, token, wanted)
			if persistent && (st.Result != nil || st.Progress.Percent == 100 || h.manager.Ready()) {
				t.Fatal("failed persistence reported durable success")
			}
			_, _, _, drafts := w.counts()
			if drafts != 1 {
				t.Fatal("draft resent after local fault")
			}
			h.close()
			c.Fault = nil
			h = startHarness(t, w, c)
			s, out = h.get(p, token)
			expect(t, s, out, 200, "")
			if task(out).Status != wanted {
				t.Fatalf("restart %+v", task(out))
			}
			_, _, _, drafts = w.counts()
			if drafts != 1 {
				t.Fatal("restart resent draft")
			}
		})
	}
}

func TestHTTPConcurrentDiskReservationAndRollbackResidue(t *testing.T) {
	w := newWX(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var block atomic.Bool
	block.Store(true)
	c := config(t)
	c.DiskQuota = logic.StateReserve + logic.UploadReservation + logic.MiB
	c.Fault = func(op, path string) error {
		if op == "imagewrite" && block.Load() {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}
		return nil
	}
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", false)
	done := make(chan int, 1)
	go func() { s, _ := h.post(p, token, files, false); done <- s }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not enter")
	}
	q, token2, files2 := payload("wx1111111111111111", false)
	s, out := h.post(q, token2, files2, false)
	expect(t, s, out, 503, "STORAGE_UNAVAILABLE")
	if h.manager.Stats()["diskReserved"] != logic.UploadReservation {
		t.Fatal("budget not reserved atomically")
	}
	block.Store(false)
	close(release)
	if <-done != 202 {
		t.Fatal("first request failed")
	}
	h.wait(p, token, "succeeded")
	if h.manager.Stats()["diskReserved"] != 0 {
		t.Fatal("unused reservation not released")
	}
	h.close()
	var fault atomic.Bool
	fault.Store(true)
	c = config(t)
	c.Fault = func(op, path string) error {
		if fault.Load() && (op == "imagewrite" || op == "remove") {
			return errors.New("rollback fault")
		}
		return nil
	}
	h = startHarness(t, w, c)
	p, token, files = payload("wx2222222222222222", false)
	s, out = h.post(p, token, files, false)
	expect(t, s, out, 503, "STORAGE_UNAVAILABLE")
	stats := h.manager.Stats()
	if stats["diskReserved"] != 0 || stats["diskUsed"] <= 0 {
		t.Fatal("residue not converted to actual usage", stats)
	}
	fault.Store(false)
	h.manager.Cleanup()
	entries, _ := os.ReadDir(filepath.Join(c.DataDir, ".staging"))
	if len(entries) != 0 || h.manager.Stats()["diskUsed"] != 0 {
		t.Fatal("residual budget not reclaimed")
	}
}

func TestHTTPRecoveryMissingCorruptInputsAndIdentity(t *testing.T) {
	for _, damage := range []string{"missing-status", "missing-input", "corrupt-status", "corrupt-receipt", "digest-mismatch", "unknown-phase"} {
		t.Run(damage, func(t *testing.T) {
			c := config(t)
			w := newWX(t)
			h := startHarness(t, w, c)
			p, token, files := payload("wx0123456789abcdef", true)
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 202, "")
			h.wait(p, token, "succeeded")
			dir := taskDir(h, p)
			h.close()
			switch damage {
			case "missing-status":
				os.Remove(filepath.Join(dir, "status.json"))
			case "missing-input":
				os.Remove(filepath.Join(dir, "images", "body.png"))
			case "corrupt-status":
				os.WriteFile(filepath.Join(dir, "status.json"), []byte("{"), 0600)
			case "corrupt-receipt":
				os.WriteFile(filepath.Join(dir, "receipt.json"), []byte("{"), 0600)
			case "digest-mismatch":
				os.WriteFile(filepath.Join(dir, "original.html"), []byte("changed"), 0600)
			case "unknown-phase":
				b, _ := os.ReadFile(filepath.Join(dir, "status.json"))
				b = []byte(strings.Replace(string(b), `"phase":"done"`, `"phase":"unknown-stage"`, 1))
				os.WriteFile(filepath.Join(dir, "status.json"), b, 0600)
			}
			h = startHarness(t, w, c)
			s, out = h.get(p, token)
			if damage == "corrupt-receipt" {
				expect(t, s, out, 404, "TASK_NOT_FOUND")
			} else {
				expect(t, s, out, 200, "")
				st := task(out)
				if st.Status != "uncertain" || st.Phase != "recovery_unknown" || st.RetryAllowed {
					t.Fatalf("unsafe recovery %+v", st)
				}
			}
			marker, err := os.ReadFile(filepath.Join(dir, "recovery.json"))
			if err != nil {
				t.Fatal(err)
			}
			h.close()
			h = startHarness(t, w, c)
			again, _ := os.ReadFile(filepath.Join(dir, "recovery.json"))
			if string(marker) != string(again) {
				t.Fatal("isolation deadline reset")
			}
			_, _, _, drafts := w.counts()
			if drafts != 1 {
				t.Fatal("damaged task reexecuted")
			}
			s, out = h.post(p, token, files, false)
			if damage == "corrupt-receipt" {
				expect(t, s, out, 404, "TASK_NOT_FOUND")
			} else {
				expect(t, s, out, 200, "")
			}
		})
	}
}
