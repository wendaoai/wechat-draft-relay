package wechatdraft

import (
	"log"
	"sync/atomic"
)

type counters struct {
	accepted, success, failed, cancelled, uncertain atomic.Int64
	cleanupFailures, rollbackFailures, deleted      atomic.Int64
}

func (m *Manager) recordTerminal(status string) {
	switch status {
	case "succeeded":
		m.events.success.Add(1)
	case "failed":
		m.events.failed.Add(1)
	case "cancelled":
		m.events.cancelled.Add(1)
	case "uncertain":
		m.events.uncertain.Add(1)
	}
}
func (m *Manager) logMaintenance() {
	stats := m.Stats()
	log.Printf("wechat-draft maintenance: accepted=%d succeeded=%d failed=%d cancelled=%d uncertain=%d deleted=%d cleanupFailures=%d rollbackFailures=%d diskUsed=%d diskReserved=%d active=%d",
		m.events.accepted.Load(), m.events.success.Load(), m.events.failed.Load(), m.events.cancelled.Load(), m.events.uncertain.Load(), m.events.deleted.Load(), m.events.cleanupFailures.Load(), m.events.rollbackFailures.Load(), stats["diskUsed"], stats["diskReserved"], stats["active"])
}
