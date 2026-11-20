package wechatdraft_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestHTTPServiceAndConnectionChecks(t *testing.T) {
	wx := newWX(t)
	h := startHarness(t, wx, config(t))
	for _, key := range []string{"wrong", testKey} {
		req, _ := http.NewRequest("GET", h.url+"/api/v1/wechat-drafts/service-check", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := h.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		want := 200
		if key == "wrong" {
			want = 401
		}
		if res.StatusCode != want {
			t.Fatalf("service check %d want %d", res.StatusCode, want)
		}
	}
	body, _ := json.Marshal(map[string]string{"appid": "wx1234567890123456", "appsecret": testSecret})
	req, _ := http.NewRequest("POST", h.url+"/api/v1/wechat-drafts/connection-test", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("connection check %d", res.StatusCode)
	}
	token, cover, images, drafts := wx.counts()
	if token != 1 || cover != 0 || images != 0 || drafts != 0 {
		t.Fatalf("connection test mutated wechat: %d %d %d %d", token, cover, images, drafts)
	}
}
