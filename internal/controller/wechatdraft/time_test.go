package wechatdraft_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func uuidAt(now time.Time) string {
	u := uuid.Must(uuid.NewV7())
	ms := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		u[i] = byte(ms)
		ms >>= 8
	}
	return u.String()
}
func TestHTTPUUIDTimeWindowAndClockRollback(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	c := config(t)
	c.Now = func() time.Time { return time.Unix(0, now.Load()) }
	h := startHarness(t, newWX(t), c)
	p, token, files := payload("wx0123456789abcdef", false)
	p.UUID = uuidAt(c.Now().Add(-6 * time.Minute))
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 409, "REQUEST_EXPIRED")
	p.UUID = uuidAt(c.Now().Add(time.Minute))
	s, out = h.post(p, token, files, false)
	expect(t, s, out, 422, "UUID_TIME_INVALID")
	p.UUID = uuidAt(c.Now())
	s, out = h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	h.wait(p, token, "succeeded")
	now.Add(-int64(2 * time.Second))
	q, token2, files2 := payload("wx1111111111111111", false)
	s, out = h.post(q, token2, files2, false)
	expect(t, s, out, 503, "CLOCK_UNAVAILABLE")
	// A clock rollback suspends new admission, not valid task polling.
	s, out = h.get(p, token)
	expect(t, s, out, 200, "")
	// Clock guard does not invalidate the already accepted original receipt.
	s, out = h.post(p, token, nil, false)
	expect(t, s, out, 200, "")
}

func TestHTTPCleanupEveryTerminalStatus(t *testing.T) {
	for _, mode := range []string{"ip", "lost", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			var now atomic.Int64
			now.Store(time.Now().UnixNano())
			c := config(t)
			c.Now = func() time.Time { return time.Unix(0, now.Load()) }
			w := newWX(t)
			var block chan struct{}
			if mode == "cancel" {
				block = make(chan struct{})
				w.blockToken = block
			} else {
				w.setMode(mode)
			}
			h := startHarness(t, w, c)
			p, token, files := payload("wx0123456789abcdef", true)
			s, out := h.post(p, token, files, false)
			expect(t, s, out, 202, "")
			want := "failed"
			if mode == "lost" {
				want = "uncertain"
			}
			if mode == "cancel" {
				want = "cancelled"
				s, out, _ = h.request("POST", "/api/v1/wechat-drafts/"+p.AppID+"/"+p.UUID+"/cancel", token, p.AppID, p.UUID, "application/json", strings.NewReader(`{"expectedAttempt":1}`))
				expect(t, s, out, 200, "")
				close(block)
			}
			st := h.wait(p, token, want)
			now.Store(st.FinishedAt.Add(24 * time.Hour).UnixNano())
			h.manager.Cleanup()
			s, out = h.get(p, token)
			expect(t, s, out, 404, "TASK_NOT_FOUND")
			s, out = h.post(p, token, files, false)
			expect(t, s, out, 409, "REQUEST_EXPIRED")
			if h.manager.Stats()["diskUsed"] != 0 {
				t.Fatal("terminal files retained")
			}
		})
	}
}
