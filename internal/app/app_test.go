package app

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"testing"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"
)

func TestRunWithoutDatabaseAndGracefulCancellation(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(configPath, []byte("WECHAT_DRAFT_SERVICE_KEY=local-smoke-key-never-use-in-production\nGF_SERVER_ADDRESS=127.0.0.1:0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WECHAT_DRAFT_DATA_DIR", "must-not-use-system-environment")
	t.Setenv("WECHAT_DRAFT_SERVICE_KEY", "must-not-use-system-environment")
	t.Setenv("WECHAT_DRAFT_LISTEN", "invalid")
	t.Setenv("GF_SERVER_ADDRESS", "invalid-system-address")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, configPath) }()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	s := ghttp.GetServer("wechat-draft")
	deadline := time.Now().Add(10 * time.Second)
	var base string
	for {
		select {
		case err := <-done:
			t.Fatalf("startup exited: %v", err)
		default:
		}
		port := s.GetListenedPort()
		if port > 0 {
			base = "http://127.0.0.1:" + strconv.Itoa(port)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if res.StatusCode != http.StatusOK || readErr != nil {
			t.Fatalf("%s: status=%d err=%v", path, res.StatusCode, readErr)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}
