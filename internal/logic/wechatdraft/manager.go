package wechatdraft

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

type job struct {
	app, id string
	attempt int
	secret  string
	cancel  context.CancelFunc
	running bool
}
type Upload struct {
	manager                         *Manager
	payload                         v1.Payload
	tokenHash, requestHash, stageID string
	retry                           bool
	dir                             string
	ctx                             context.Context
	cancel                          context.CancelFunc
	once                            sync.Once
	reserved                        bool
}
type Manager struct {
	config                     Config
	store                      *Store
	wechat                     *Wechat
	ctx                        context.Context
	cancel                     context.CancelFunc
	mu                         sync.Mutex
	locks                      taskLocks
	queue                      []*job
	jobs                       map[string]*job
	active                     map[string]string
	pending                    map[string]*Upload
	stages                     map[string]*Upload
	overrides                  map[string]*State
	notify                     chan struct{}
	wg                         sync.WaitGroup
	closeOnce                  sync.Once
	closeDone                  chan struct{}
	closeErr                   error
	stopped, unready           bool
	lastClock                  time.Time
	clockUnsafeUntil           time.Time
	quota                      rateLimiter
	controls, queries, cancels chan struct{}
	cleanupMu                  sync.Mutex
	cursor                     *directoryCursor
	stageCursor                *os.File
	queueWaits                 [4096]float64
	queueWaitCount             int
	events                     counters
}

func NewManager(parent context.Context, c Config, w *Wechat) (*Manager, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	store, err := openStore(c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{config: c, store: store, wechat: w, ctx: ctx, cancel: cancel, jobs: map[string]*job{}, active: map[string]string{}, pending: map[string]*Upload{}, stages: map[string]*Upload{}, overrides: map[string]*State{}, notify: make(chan struct{}, 1), controls: make(chan struct{}, 8), queries: make(chan struct{}, 32), cancels: make(chan struct{}, 4), closeDone: make(chan struct{})}
	if err = m.recover(); err != nil {
		cancel()
		store.lockFile.Close()
		return nil, errors.New("task recovery could not complete")
	}
	cleanupMore := m.Cleanup()
	for i := 0; i < c.Workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	m.wg.Add(2)
	go m.maintenance()
	go m.cleaner(cleanupMore)
	return m, nil
}
func key(app, id string) string { return app + "/" + id }
func (m *Manager) signal() {
	select {
	case m.notify <- struct{}{}:
	default:
	}
}
func (m *Manager) stoppedError() *APIError {
	return fail(503, "EXECUTOR_UNAVAILABLE", "执行器正在停止或恢复")
}
func (m *Manager) read(app, id string) (*State, *APIError) {
	m.mu.Lock()
	st := m.overrides[key(app, id)]
	m.mu.Unlock()
	if st != nil {
		return cloneState(st), nil
	}
	st, e := m.store.loadState(app, id)
	if e != nil && e.Status == 503 {
		m.markUnready()
	}
	return st, e
}
func (m *Manager) authenticated(app, id, tokenHash string) (*State, *APIError) {
	// Verify immutable identity before surfacing a storage error. A valid
	// client's temporary read failure must not masquerade as a missing task.
	r, err := m.store.receipt(m.store.path(app, id), app, id)
	if err != nil || !sameHash(r.TaskTokenHash, tokenHash) {
		return nil, notFound()
	}
	st, e := m.read(app, id)
	if e != nil {
		return nil, e
	}
	if !sameHash(st.TaskTokenHash, tokenHash) {
		return nil, notFound()
	}
	if st.ExpiresAt != nil && !m.config.now().Before(*st.ExpiresAt) {
		return nil, notFound()
	}
	return st, nil
}
func (m *Manager) Get(app, id, tokenHash string) (v1.Task, *APIError) {
	unlock := m.locks.acquire(key(app, id))
	defer unlock()
	st, e := m.authenticated(app, id, tokenHash)
	if e != nil {
		return v1.Task{}, e
	}
	return st.view(), nil
}
func (m *Manager) save(st *State) error {
	return m.store.writeJSON(m.store.path(st.AppID, st.UUID), "status.json", st, true)
}
func (m *Manager) release(j *job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(j.app, j.id)
	if m.jobs[k] != j {
		return
	}
	delete(m.jobs, k)
	if m.active[j.app] == k {
		delete(m.active, j.app)
	}
	j.secret = ""
	for i, item := range m.queue {
		if item == j {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			break
		}
	}
}
func (m *Manager) Ready() bool {
	m.mu.Lock()
	ready := !m.stopped && !m.unready
	m.mu.Unlock()
	if !ready || m.ctx.Err() != nil || !m.store.budget.available(0, m.config.DataDir) {
		return false
	}
	f, err := os.CreateTemp(m.config.DataDir, ".ready-*")
	if err != nil {
		return false
	}
	path := f.Name()
	_, err = f.Write([]byte("ready"))
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	removeErr := os.Remove(path)
	if removeErr != nil {
		if info, e := os.Stat(path); e == nil {
			m.store.budget.adjust(info.Size())
		}
	}
	return err == nil && removeErr == nil
}
func (m *Manager) Close(ctx context.Context) error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.stopped = true
		m.mu.Unlock()
		m.cancel()
		m.signal()
		go m.closeResources()
	})
	// Disk I/O and task locks can stall. The caller's deadline bounds its wait,
	// while the single shutdown operation retains ownership until I/O finishes.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.closeDone:
		return m.closeErr
	}
}
func (m *Manager) markUnready() { m.mu.Lock(); m.unready = true; m.mu.Unlock() }
func (m *Manager) Stats() map[string]int64 {
	used, reserved := m.store.budget.snapshot()
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]int64{"diskUsed": used, "diskReserved": reserved, "queue": int64(len(m.queue)), "uploads": int64(len(m.pending)), "active": int64(len(m.jobs)), "overrides": int64(len(m.overrides)), "cleanupFailures": m.events.cleanupFailures.Load(), "rollbackFailures": m.events.rollbackFailures.Load(), "deleted": m.events.deleted.Load()}
}
func (m *Manager) existingDirectory(app, id string) bool {
	for _, name := range []string{id, ".gc-" + id} {
		if _, err := os.Lstat(filepath.Join(m.config.DataDir, app, name)); !os.IsNotExist(err) {
			return true
		}
	}
	return false
}

// Diagnostics are bounded and contain no content or task credentials.
func (m *Manager) QueueWaitSamples() []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]float64(nil), m.queueWaits[:min(m.queueWaitCount, len(m.queueWaits))]...)
}
