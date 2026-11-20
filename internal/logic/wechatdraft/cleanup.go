package wechatdraft

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func (m *Manager) recover() error {
	if err := eachEntry(m.config.DataDir, func(e os.DirEntry) error {
		if !e.IsDir() && strings.HasPrefix(e.Name(), ".ready-") {
			freed, _ := m.store.remove(filepath.Join(m.config.DataDir, e.Name()))
			m.store.budget.adjust(-freed)
		}
		return nil
	}); err != nil {
		return err
	}
	stageRoot := filepath.Join(m.config.DataDir, ".staging")
	if err := eachEntry(stageRoot, func(e os.DirEntry) error {
		if m.ctx.Err() != nil {
			return m.ctx.Err()
		}
		freed, _ := m.store.remove(filepath.Join(stageRoot, e.Name()))
		m.store.budget.adjust(-freed)
		return nil
	}); err != nil {
		return err
	}
	cursor, err := newCursor(m.config.DataDir)
	if err != nil {
		return err
	}
	defer cursor.close()
	for {
		app, id, err := cursor.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if m.ctx.Err() != nil {
			return m.ctx.Err()
		}
		if strings.HasPrefix(id, ".gc-") {
			continue
		}
		if _, _, e := Identity(app, id); e != nil {
			_, err = m.store.isolate(m.store.path(app, id))
		} else {
			err = m.store.recoverOne(app, id)
		}
		if err != nil {
			return err
		}
	}
}

// Cleanup advances a bounded cursor, at most 100 tasks between lifecycle checks
// and 30 seconds per invocation. An unfinished pass resumes after five seconds.
func (m *Manager) Cleanup() bool {
	m.cleanupMu.Lock()
	defer m.cleanupMu.Unlock()
	if m.cursor == nil {
		var err error
		m.cursor, err = newCursor(m.config.DataDir)
		if err != nil {
			m.markUnready()
			return false
		}
	}
	started := time.Now()
	for batch := 0; time.Since(started) < 30*time.Second; batch++ {
		for n := 0; n < 100; n++ {
			if m.ctx.Err() != nil {
				return true
			}
			app, id, err := m.cursor.next()
			if err == io.EOF {
				m.cursor.close()
				m.cursor = nil
				return m.cleanStages(started.Add(30 * time.Second))
			}
			if err != nil {
				m.markUnready()
				m.cursor.close()
				m.cursor = nil
				return false
			}
			m.cleanOne(app, id)
			if time.Since(started) >= 30*time.Second {
				return true
			}
		}
	}
	return true
}
func (m *Manager) cleanOne(app, id string) {
	original := strings.TrimPrefix(id, ".gc-")
	unlock := m.locks.acquire(key(app, original))
	dir := m.store.path(app, id)
	m.mu.Lock()
	active := m.jobs[key(app, original)] != nil || m.pending[key(app, original)] != nil
	m.mu.Unlock()
	if active {
		unlock()
		return
	}
	gc := strings.HasPrefix(id, ".gc-")
	if !gc {
		var rec Recovery
		var expiry *time.Time
		if err := m.store.readJSON(dir, "recovery.json", &rec); !os.IsNotExist(err) {
			marker, err := m.store.isolate(dir)
			if err != nil {
				m.markUnready()
				unlock()
				return
			}
			expiry = &marker.ExpiresAt
		} else {
			st, e := m.read(app, id)
			if e == nil && terminal(st) {
				expiry = st.ExpiresAt
			} else if e != nil {
				isolation, err := m.store.isolate(dir)
				if err != nil {
					m.markUnready()
					unlock()
					return
				}
				expiry = &isolation.ExpiresAt
			}
		}
		if expiry == nil || m.config.now().Before(*expiry) {
			unlock()
			return
		}
		dest := m.store.path(app, ".gc-"+id)
		if err := m.store.safe(dest); err != nil {
			unlock()
			return
		}
		if err := m.store.inject("gc", dir); err != nil {
			m.events.cleanupFailures.Add(1)
			unlock()
			return
		}
		if err := commitRename(dir, dest); err != nil {
			m.events.cleanupFailures.Add(1)
			unlock()
			return
		}
		if syncDirectory(filepath.Dir(dest)) != nil {
			m.markUnready()
		}
		dir = dest
	}
	m.mu.Lock()
	delete(m.overrides, key(app, original))
	m.mu.Unlock()
	m.store.receipts.remove(m.store.path(app, original))
	unlock()
	freed, err := m.store.remove(dir)
	if err != nil {
		m.events.cleanupFailures.Add(1)
	} else {
		m.events.deleted.Add(1)
	}
	m.store.budget.adjust(-freed)
}

func (m *Manager) expireQueue() {
	m.mu.Lock()
	items := append([]*job(nil), m.queue...)
	m.mu.Unlock()
	for _, j := range items {
		unlock := m.locks.acquire(key(j.app, j.id))
		st, e := m.read(j.app, j.id)
		if e == nil && st.Attempt == j.attempt && st.Status == "queued" && m.config.now().Sub(st.QueuedAt) >= m.config.QueueTimeout {
			m.release(j)
			setFinished(st, "failed", &v1.Fault{Code: "QUEUE_TIMEOUT", Message: "排队超时，请稍后显式重试"}, m.config.now())
			m.persistFinal(st)
		}
		unlock()
	}
}
