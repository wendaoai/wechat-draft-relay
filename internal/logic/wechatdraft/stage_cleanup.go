package wechatdraft

import (
	"io"
	"os"
	"path/filepath"
	"time"
)

// Caller holds cleanupMu. Yield after at most 100 residual stages rather than
// letting an unbounded rollback backlog monopolize the cleaner.
func (m *Manager) cleanStages(deadline time.Time) bool {
	root := filepath.Join(m.config.DataDir, ".staging")
	if m.stageCursor == nil {
		var err error
		m.stageCursor, err = os.Open(root)
		if err != nil {
			m.markUnready()
			return false
		}
	}
	for n := 0; n < 100; n++ {
		if m.ctx.Err() != nil || !time.Now().Before(deadline) {
			return true
		}
		entries, err := m.stageCursor.ReadDir(1)
		if len(entries) == 0 {
			m.stageCursor.Close()
			m.stageCursor = nil
			if err != io.EOF {
				m.markUnready()
			}
			return false
		}
		entry := entries[0]
		m.mu.Lock()
		active := m.stages[entry.Name()] != nil
		m.mu.Unlock()
		if !active {
			freed, removeErr := m.store.remove(filepath.Join(root, entry.Name()))
			m.store.budget.adjust(-freed)
			if removeErr != nil {
				m.events.cleanupFailures.Add(1)
			}
		}
	}
	return true
}
