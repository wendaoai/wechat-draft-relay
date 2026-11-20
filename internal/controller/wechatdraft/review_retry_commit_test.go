package wechatdraft_test

import (
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestHTTPRetrySaveFailureCannotLeaveUnownedQueue(t *testing.T) {
	for _, boundary := range []string{"sync", "dirsync", "dirsync-and-final-write"} {
		t.Run(boundary, func(t *testing.T) {
			w, c := newWX(t), config(t)
			w.setMode("ip")
			p, token, files := payload("wx0123456789abcdef", false)
			path := filepath.Join(c.DataDir, p.AppID, p.UUID, "status.json")
			var armed, triggered atomic.Bool
			c.Fault = func(op, target string) error {
				if !armed.Load() || target != path {
					return nil
				}
				if boundary == "dirsync-and-final-write" && triggered.Load() && op == "write" {
					return errors.New("final state write unavailable")
				}
				want := boundary
				if boundary == "dirsync-and-final-write" {
					want = "dirsync"
				}
				if op == want && triggered.CompareAndSwap(false, true) {
					return errors.New("retry state persistence failure")
				}
				return nil
			}
			h := startHarness(t, w, c)
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 202, "")
			h.wait(p, token, "failed")
			p.ExpectedAttempt, p.RetryRequestID = 1, uuid.NewString()
			armed.Store(true)
			s, out = h.post(p, token, files, true)
			expect(t, s, out, 503, "STORAGE_UNAVAILABLE")
			if !triggered.Load() {
				t.Fatal("target storage fault was not exercised")
			}
			s, out = h.get(p, token)
			expect(t, s, out, 200, "")
			st := task(out)
			if st.Status == "queued" || st.Status == "running" {
				t.Fatalf("retry left an unowned executable state: %+v stats=%v", st, h.manager.Stats())
			}
			if boundary == "sync" {
				if st.Attempt != 1 || st.Status != "failed" || st.LastRetry != nil {
					t.Fatal("failed atomic replacement changed original attempt", st)
				}
			} else {
				if st.Attempt != 2 || st.LastRetry == nil || st.LastRetry.RequestID != p.RetryRequestID {
					t.Fatal("visible retry operation was lost", st)
				}
				want := "failed"
				if boundary == "dirsync-and-final-write" {
					want = "uncertain"
				}
				if st.Status != want || st.RetryAllowed != (want == "failed") {
					t.Fatal("wrong retry failure disposition", st)
				}
				s, out = h.post(p, token, nil, true)
				expect(t, s, out, 200, "")
				if task(out).Attempt != 2 || task(out).Status != want {
					t.Fatal("replayed operation changed terminal round", task(out))
				}
			}
			stats := h.manager.Stats()
			if stats["active"] != 0 || stats["queue"] != 0 || stats["uploads"] != 0 || stats["diskReserved"] != 0 {
				t.Fatal("failed retry leaked resources", stats)
			}
			tokens, _, _, drafts := w.counts()
			if tokens != 1 || drafts != 0 {
				t.Fatal("failed persistence called WeChat", tokens, drafts)
			}
			armed.Store(false)
			h.close()
			h = startHarness(t, w, c)
			s, out = h.get(p, token)
			expect(t, s, out, 200, "")
			if task(out).Status != "failed" {
				t.Fatal("restart did not preserve/recover safe failure", task(out))
			}
			// A fresh explicit operation after recovery may execute, exactly once.
			p.ExpectedAttempt = task(out).Attempt
			p.RetryRequestID = uuid.NewString()
			w.setMode("")
			s, out = h.post(p, token, files, true)
			expect(t, s, out, 202, "")
			h.wait(p, token, "succeeded")
			_, _, _, drafts = w.counts()
			if drafts != 1 {
				t.Fatal("fresh operation did not create exactly one draft", drafts)
			}
		})
	}
}
