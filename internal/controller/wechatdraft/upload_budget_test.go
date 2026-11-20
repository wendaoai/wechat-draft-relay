package wechatdraft_test

import (
	"net/http"
	"strings"
	"testing"

	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

func TestHTTPUploadResourceBudgets(t *testing.T) {
	w := newWX(t)
	h := startHarness(t, w, config(t))
	p, token, _ := payload("wx0123456789abcdef", false)
	// Reject an oversized announced request before reading or reserving disk.
	req, err := http.NewRequest("POST", h.url+"/api/v1/wechat-drafts", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = logic.MaxRequest + 1
	req.Header.Set("Expect", "100-continue")
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("X-Draft-Token", token)
	req.Header.Set("X-Wechat-AppId", p.AppID)
	req.Header.Set("X-Draft-UUID", p.UUID)
	res, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized transport status: %d", res.StatusCode)
	}

	_, cover, images, drafts := w.counts()
	if cover != 0 || images != 0 || drafts != 0 || h.manager.Stats()["diskReserved"] != 0 {
		t.Fatal("rejected request consumed upload capacity or called WeChat")
	}
}
