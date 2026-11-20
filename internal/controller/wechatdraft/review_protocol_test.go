package wechatdraft_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

func TestHTTPCommitSyncAndFailureStateFaultCannotLeaveTaskQueued(t *testing.T) {
	w, c := newWX(t), config(t)
	var committed atomic.Bool
	c.Fault = func(op, path string) error {
		if op == "commitsync" {
			committed.Store(true)
			return errors.New("commit sync failure")
		}
		if committed.Load() && op == "write" && strings.HasSuffix(path, "status.json") {
			return errors.New("failure state save failure")
		}
		return nil
	}
	h := startHarness(t, w, c)
	p, token, files := payload("wx0123456789abcdef", false)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 503, "STORAGE_UNAVAILABLE")
	s, out = h.get(p, token)
	expect(t, s, out, 200, "")
	if st := task(out); st.Status != "uncertain" || st.RetryAllowed {
		t.Fatalf("unexecuted task stuck in queued after failed commit: %+v", st)
	}
	_, _, _, drafts := w.counts()
	if drafts != 0 {
		t.Fatal("incomplete commit called Wechat")
	}
}

func TestHTTPReadinessRejectsExhaustedStateHeadroom(t *testing.T) {
	w, c := newWX(t), config(t)
	c.DiskQuota = logic.StateReserve + logic.UploadReservation
	dir := filepath.Join(c.DataDir, "wx0123456789abcdef", uuid.Must(uuid.NewV7()).String())
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "orphan-input"))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(logic.UploadReservation + 1)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	h := startHarness(t, w, c)
	r, err := h.client.Get(h.url + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("full state headroom reported ready", r.StatusCode)
	}
	r, err = h.client.Get(h.url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatal("storage shortage is not process death")
	}
}
