package wechatdraft_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedLocalTenThousandIsolatedCleanup(t *testing.T) {
	if os.Getenv("DRAFT_LOAD_TEST") != "1" {
		t.Skip("explicit bounded local load only")
	}
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	c := config(t)
	c.Now = func() time.Time { return time.Unix(0, now.Load()) }
	app := "wx0123456789abcdef"
	root := filepath.Join(c.DataDir, app)
	os.Mkdir(root, 0700)
	// Real orphan input directories exercise recovery and cleanup without
	// inventing successful tasks or writing fake media_id receipts.
	for i := 0; i < 10000; i++ {
		dir := filepath.Join(root, uuidAt(c.Now().Add(time.Duration(i)*time.Millisecond)))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "original.html"), []byte("<p>orphan</p>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w := newWX(t)
	start := time.Now()
	h := startHarness(t, w, c)
	recoverySeconds := time.Since(start).Seconds()
	now.Add(int64(24 * time.Hour))
	done := make(chan int, 1)
	start = time.Now()
	go func() {
		passes := 1
		for h.manager.Cleanup() {
			passes++
		}
		done <- passes
	}()
	p, token, files := payload("wx1111111111111111", true)
	p.UUID = uuidAt(c.Now())
	begin := time.Now()
	s, out := h.post(p, token, files, false)
	expect(t, s, out, 202, "")
	createMS := float64(time.Since(begin).Microseconds()) / 1000
	st := h.wait(p, token, "succeeded")
	if st.Result == nil {
		t.Fatal("normal task blocked by cleanup")
	}
	var passes int
	select {
	case passes = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("cleanup budget did not converge")
	}
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("isolated directories were not fully reclaimed")
	}
	_, _, _, drafts := w.counts()
	if drafts != 1 {
		t.Fatal("isolated recovery called Wechat")
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	report := map[string]any{"directories": 10000, "recoverySeconds": recoverySeconds, "cleanupSeconds": time.Since(start).Seconds(), "passes": passes, "batchLimit": 100, "concurrentCreateLatencyMS": createMS, "heapAtEndBytes": mem.HeapAlloc, "goroutinesAtEnd": runtime.NumGoroutine(), "remainingOriginalTasks": len(entries), "simulatedWechatDrafts": drafts}
	b, _ := json.MarshalIndent(report, "", "  ")
	t.Log(string(b))
	if path := os.Getenv("DRAFT_CLEANUP_REPORT"); path != "" {
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
