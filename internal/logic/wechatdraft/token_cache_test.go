package wechatdraft

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRefreshAfterEvictionKeepsTokenCacheBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"replacement-token","expires_in":7200}`)
	}))
	defer server.Close()
	w := NewLocalWechat(server.URL)
	defer w.Close()
	for i := 0; i < 256; i++ {
		w.cache.entries[fmt.Sprint(i)] = tokenEntry{token: "cached", expiry: time.Now().Add(time.Hour), used: time.Now()}
	}
	token, err := w.Refresh(context.Background(), "wx0123456789abcdef", "memory-secret", "evicted-token")
	if err != nil || token != "replacement-token" {
		t.Fatal("real token refresh failed", err)
	}
	if len(w.cache.entries) > 256 {
		t.Fatalf("refresh grew bounded cache to %d entries", len(w.cache.entries))
	}
}
