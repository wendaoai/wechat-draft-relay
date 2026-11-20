package wechatdraft

import v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"

func (m *Manager) closeResources() {
	defer close(m.closeDone)
	m.shutdownQueued()
	m.wg.Wait()
	m.cleanupMu.Lock()
	if m.cursor != nil {
		m.cursor.close()
		m.cursor = nil
	}
	if m.stageCursor != nil {
		m.stageCursor.Close()
		m.stageCursor = nil
	}
	m.cleanupMu.Unlock()
	m.wechat.Close()
	m.closeErr = m.store.lockFile.Close()
}

func (m *Manager) shutdownQueued() {
	m.mu.Lock()
	queued := append([]*job(nil), m.queue...)
	m.mu.Unlock()
	for _, j := range queued {
		unlock := m.locks.acquire(key(j.app, j.id))
		st, e := m.read(j.app, j.id)
		if e == nil && st.Status == "queued" && st.Attempt == j.attempt {
			setFinished(st, "failed", &v1.Fault{Code: "SERVER_RESTARTED", Message: "服务已停止，请重新提供凭据后显式重试"}, m.config.now())
			m.persistFinal(st)
			m.release(j)
		}
		unlock()
	}
}
