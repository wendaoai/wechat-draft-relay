package wechatdraft

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStagingCleanupHasBoundedBatchesAndAccurateBudget(t *testing.T) {
	c := DefaultConfig()
	c.DataDir, c.ServiceKey = t.TempDir(), "private-test-service-key-at-least32"
	m, err := NewManager(context.Background(), c, NewWechat(1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 201; i++ {
		dir := filepath.Join(c.DataDir, ".staging", fmt.Sprint(i))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := m.store.writeFile(dir, "input", []byte("residual upload"), true); err != nil {
			t.Fatal(err)
		}
	}
	if more := m.Cleanup(); !more {
		t.Fatal("staging backlog was drained without yielding after 100 entries")
	}
	entries, err := os.ReadDir(filepath.Join(c.DataDir, ".staging"))
	if err != nil || len(entries) < 101 {
		t.Fatal("unbounded stage cleanup", len(entries), err)
	}
	for pass := 0; m.Cleanup(); pass++ {
		if pass > 4 {
			t.Fatal("staging cleanup failed to finish")
		}
	}
	entries, err = os.ReadDir(filepath.Join(c.DataDir, ".staging"))
	if err != nil || len(entries) != 0 || m.Stats()["diskUsed"] != 0 {
		t.Fatal("staging cleanup leaked files or budget", err)
	}
}
