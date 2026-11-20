package wechatdraft

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

func (s *Store) isolate(dir string) (*Recovery, error) {
	var recovery Recovery
	err := s.readJSON(dir, "recovery.json", &recovery)
	if err == nil && !recovery.FirstIsolatedAt.IsZero() && recovery.ExpiresAt.Equal(recovery.FirstIsolatedAt.Add(24*time.Hour)) {
		return &recovery, nil
	}
	now := s.config.now()
	first := now
	if err == nil && !recovery.FirstIsolatedAt.IsZero() {
		first = recovery.FirstIsolatedAt
	} else if !os.IsNotExist(err) {
		// A malformed marker is recoverable; inaccessible files are not.
		// Never turn permission/I/O failures into a fresh retention window.
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return nil, err
		}
		info, statErr := os.Stat(filepath.Join(dir, "recovery.json"))
		if statErr != nil {
			return nil, statErr
		}
		first = info.ModTime().In(shanghai)
		if r, receiptErr := s.receipt(dir, filepath.Base(filepath.Dir(dir)), filepath.Base(dir)); receiptErr == nil {
			// Lost isolation time cannot justify extending a trusted task's
			// retention. Use its immutable admission time conservatively.
			first = r.AcceptedAt
		}
	}
	if first.After(now) {
		first = now
	}
	recovery = Recovery{FirstIsolatedAt: first, ExpiresAt: first.Add(24 * time.Hour)}
	if err := s.writeJSON(dir, "recovery.json", recovery, true); err != nil {
		return nil, err
	}
	return &recovery, nil
}
