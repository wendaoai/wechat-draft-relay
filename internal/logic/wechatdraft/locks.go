package wechatdraft

import "sync"

type lockEntry struct {
	mu   sync.Mutex
	refs int
}
type taskLocks struct {
	mu      sync.Mutex
	entries map[string]*lockEntry
}

// Count waiting callers as well as the owner. The entry can never disappear
// while another caller is about to acquire this same mutex.
func (l *taskLocks) acquire(key string) func() {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*lockEntry)
	}
	e := l.entries[key]
	if e == nil {
		e = &lockEntry{}
		l.entries[key] = e
	}
	e.refs++
	l.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		l.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(l.entries, key)
		}
		l.mu.Unlock()
	}
}
