package wechatdraft_test

import (
	"strings"
	"testing"
)

func TestHTTPCancelRejectsTruncatedOversizedJSON(t *testing.T) {
	w := newWX(t)
	block := make(chan struct{})
	w.blockToken = block
	defer close(block)
	h := startHarness(t, w, config(t))
	p, token, files := payload("wx0123456789abcdef", false)
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	path := "/api/v1/wechat-drafts/" + p.AppID + "/" + p.UUID + "/cancel"
	valid := `{"expectedAttempt":1}`
	for _, suffix := range []string{"", `{"expectedAttempt":2}`, "invalid trailing body"} {
		body := valid + strings.Repeat(" ", 1025-len(valid)) + suffix
		s, out, _ = h.request("POST", path, token, p.AppID, p.UUID, "application/json", strings.NewReader(body))
		expect(t, s, out, 422, "VALIDATION_FAILED")
	}
	s, out = h.get(p, token)
	expect(t, s, out, 200, "")
	if got := task(out); !got.CancelAllowed || got.Status != "queued" && got.Status != "running" {
		t.Fatal("invalid body cancelled task", got)
	}
	// The exact supported size with whitespace remains valid.
	body := valid + strings.Repeat(" ", 1024-len(valid))
	s, out, _ = h.request("POST", path, token, p.AppID, p.UUID, "application/json", strings.NewReader(body))
	expect(t, s, out, 200, "")
	st := h.wait(p, token, "cancelled")
	if st.Error == nil || st.Error.Code != "TASK_CANCELLED" || st.Result != nil {
		t.Fatal("valid cancel did not finish", st)
	}
}
