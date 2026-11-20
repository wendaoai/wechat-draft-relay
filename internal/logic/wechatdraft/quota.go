package wechatdraft

import (
	"sync"
	"time"
)

type rateEntry struct {
	start, seen time.Time
	count       int
}
type rateLimiter struct {
	mu        sync.Mutex
	entries   map[string]rateEntry
	lastSweep time.Time
	tokens    float64
	tokenAt   time.Time
}

func (r *rateLimiter) allow(key string, limit int, window time.Duration, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]rateEntry{}
	}
	if now.Sub(r.lastSweep) > time.Minute {
		for k, e := range r.entries {
			if now.Sub(e.seen) > 10*time.Minute {
				delete(r.entries, k)
			}
		}
		r.lastSweep = now
	}
	e, ok := r.entries[key]
	if !ok && len(r.entries) >= 32768 {
		return false
	}
	if !ok || now.Sub(e.start) >= window {
		e = rateEntry{start: now}
	}
	e.seen = now
	if e.count >= limit {
		r.entries[key] = e
		return false
	}
	e.count++
	r.entries[key] = e
	return true
}
func (r *rateLimiter) queryGlobal(rate int, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tokenAt.IsZero() {
		r.tokenAt = now
		r.tokens = float64(rate * 2)
	}
	// Wall-clock rollback must not spend the existing query balance or grant
	// extra refill when the clock catches up. Existing tasks remain readable.
	if now.After(r.tokenAt) {
		r.tokens += now.Sub(r.tokenAt).Seconds() * float64(rate)
		r.tokenAt = now
	}
	if r.tokens > float64(rate*2) {
		r.tokens = float64(rate * 2)
	}
	if r.tokens < 1 {
		return false
	}
	r.tokens--
	return true
}

func (m *Manager) Gate(operation, ip string) (func(), *APIError) {
	var slots chan struct{}
	limit := m.config.IPWriteLimit
	code := "CONTROL_RATE_LIMIT"
	switch operation {
	case "query":
		slots = m.queries
		limit = m.config.IPQueryLimit
		code = "QUERY_RATE_LIMIT"
	case "cancel":
		slots = m.cancels
		limit = m.config.IPCancelLimit
		code = "CANCEL_RATE_LIMIT"
	default:
		slots = m.controls
	}
	if !m.quota.allow("ip:"+operation+":"+ip, limit, time.Minute, m.config.now()) {
		return nil, busy(code)
	}
	if operation == "query" && !m.quota.queryGlobal(m.config.QueryRate, m.config.now()) {
		return nil, busy(code)
	}
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	default:
		return nil, busy(code)
	}
}

// Only call after task authentication, so random task IDs cannot expand quotas.
func (m *Manager) TaskQuota(app, id, operation string) *APIError {
	limit, code := 60, "QUERY_RATE_LIMIT"
	if operation == "cancel" {
		limit, code = 10, "CANCEL_RATE_LIMIT"
	}
	if !m.quota.allow("task:"+operation+":"+key(app, id), limit, time.Minute, m.config.now()) {
		return busy(code)
	}
	return nil
}
