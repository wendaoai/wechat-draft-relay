package wechatdraft

import "time"

// Queue deadlines must keep advancing even while filesystem cleanup is slow.
func (m *Manager) maintenance() {
	defer m.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tick.C:
			m.expireQueue()
		}
	}
}

func (m *Manager) cleaner(more bool) {
	defer m.wg.Done()
	interval := m.config.CleanupInterval
	if more {
		interval = 5 * time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-timer.C:
			interval = m.config.CleanupInterval
			if m.Cleanup() {
				interval = 5 * time.Second
			}
			m.logMaintenance()
			timer.Reset(interval)
		}
	}
}
