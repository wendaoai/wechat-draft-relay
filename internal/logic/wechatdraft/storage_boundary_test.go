package wechatdraft

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestAtomicStateReplacementPreservesOldJSONOnENOSPC(t *testing.T) {
	var fault atomic.Bool
	c := DefaultConfig()
	c.DataDir = t.TempDir()
	c.Fault = func(op, path string) error {
		if fault.Load() && op == "sync" {
			return syscall.ENOSPC
		}
		return nil
	}
	s, err := openStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.lockFile.Close()
	if err = s.writeJSON(c.DataDir, "state-probe.json", map[string]int{"attempt": 1}, true); err != nil {
		t.Fatal(err)
	}
	fault.Store(true)
	if err = s.writeJSON(c.DataDir, "state-probe.json", map[string]int{"attempt": 2}, true); !errors.Is(err, syscall.ENOSPC) {
		t.Fatal("fault was not exercised")
	}
	var result map[string]int
	if s.readJSON(c.DataDir, "state-probe.json", &result) != nil || result["attempt"] != 1 {
		t.Fatal("old state was lost")
	}
}
func TestCommitRejectsForeignTemporaryDirectory(t *testing.T) {
	c := DefaultConfig()
	c.DataDir = t.TempDir()
	s, err := openStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.lockFile.Close()
	foreign := t.TempDir()
	os.WriteFile(filepath.Join(foreign, "input"), []byte("foreign-volume-source"), 0600)
	if s.commit(foreign, s.path("wx0123456789abcdef", "task")) == nil {
		t.Fatal("foreign source renamed directly")
	}
	if _, err = os.Stat(filepath.Join(foreign, "input")); err != nil {
		t.Fatal("foreign source modified")
	}
}
func TestLimiterHasBoundedKeysAndIndependentOperations(t *testing.T) {
	var limiter rateLimiter
	now := time.Now()
	for n := 0; n < 32768; n++ {
		if !limiter.allow(string(rune(n))+"query", 1, time.Minute, now) {
			t.Fatal("capacity rejected early")
		}
	}
	if limiter.allow("overflow", 1, time.Minute, now) {
		t.Fatal("unbounded counter cache")
	}
	if !limiter.allow("cancel", 10, time.Minute, now.Add(11*time.Minute)) {
		t.Fatal("expired counters not released")
	}
}
func TestPersistedStateOmitsComputedFlags(t *testing.T) {
	b, err := json.Marshal(State{})
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	json.Unmarshal(b, &obj)
	for _, key := range []string{"retryAllowed", "cancelAllowed", "pollAfterSeconds"} {
		if _, ok := obj[key]; ok {
			t.Fatal("computed field persisted", key)
		}
	}
}

func TestExistingUserDirectoryIsRejectedWithoutChangingFiles(t *testing.T) {
	c := DefaultConfig()
	c.DataDir = t.TempDir()
	path := filepath.Join(c.DataDir, "keep-user-data.txt")
	os.WriteFile(path, []byte("keep"), 0600)
	if s, err := openStore(c); err == nil {
		s.lockFile.Close()
		t.Fatal("unrelated directory was adopted")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "keep" {
		t.Fatal("user data changed")
	}
}
