package wechatdraft

import (
	"context"
	"sync"
	"time"
)

type tokenEntry struct {
	token                  string
	expiry, used, forcedAt time.Time
}
type tokenCache struct {
	mu      sync.Mutex
	entries map[string]tokenEntry
	loading map[string]chan struct{}
	forced  map[string]time.Time
}

func (w *Wechat) Token(ctx context.Context, app, secret string) (string, *WechatError) {
	return w.token(ctx, app, secret, 3)
}

func (w *Wechat) token(ctx context.Context, app, secret string, attempts int) (string, *WechatError) {
	key := app + ":" + digest([]byte(secret))
	for {
		w.cache.mu.Lock()
		entry, ok := w.cache.entries[key]
		if ok && time.Now().Before(entry.expiry) {
			entry.used = time.Now()
			w.cache.entries[key] = entry
			w.cache.mu.Unlock()
			return entry.token, nil
		}
		if wait, ok := w.cache.loading[key]; ok {
			w.cache.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return "", wxUnavailable(false)
			}
		}
		wait := make(chan struct{})
		w.cache.loading[key] = wait
		w.cache.mu.Unlock()
		token, ttl, err := w.tokenRequest(ctx, app, secret, false, attempts)
		w.cache.mu.Lock()
		delete(w.cache.loading, key)
		if err == nil && ttl > 0 {
			w.evictToken(key)
			w.cache.entries[key] = tokenEntry{token: token, expiry: time.Now().Add(ttl), used: time.Now(), forcedAt: entry.forcedAt}
		}
		close(wait)
		w.cache.mu.Unlock()
		return token, err
	}
}

// Caller holds cache.mu. Bound both tokens and credential-specific cooldowns.
func (w *Wechat) evictToken(key string) {
	if _, ok := w.cache.entries[key]; ok || len(w.cache.entries) < 256 {
		return
	}
	oldKey := ""
	var oldest time.Time
	for k, e := range w.cache.entries {
		if oldKey == "" || e.used.Before(oldest) {
			oldKey, oldest = k, e.used
		}
	}
	delete(w.cache.entries, oldKey)
}
func (w *Wechat) Refresh(ctx context.Context, app, secret, invalid string) (string, *WechatError) {
	return w.refresh(ctx, app, secret, invalid, 3)
}

func (w *Wechat) refresh(ctx context.Context, app, secret, invalid string, attempts int) (string, *WechatError) {
	key := app + ":" + digest([]byte(secret))
	w.cache.mu.Lock()
	entry := w.cache.entries[key]
	entry.expiry = time.Time{}
	w.evictToken(key)
	w.cache.entries[key] = entry
	w.cache.mu.Unlock()
	var token string
	var err *WechatError
	if attempts > 1 {
		token, err = w.token(ctx, app, secret, attempts)
		if err != nil || token != invalid {
			return token, err
		}
	}
	w.cache.mu.Lock()
	entry = w.cache.entries[key]
	if w.cache.forced == nil {
		w.cache.forced = map[string]time.Time{}
	}
	for k, t := range w.cache.forced {
		if time.Since(t) >= 30*time.Second {
			delete(w.cache.forced, k)
		}
	}
	_, cooling := w.cache.forced[app]
	if cooling || len(w.cache.forced) >= 256 {
		w.cache.mu.Unlock()
		return "", wxCode(40014, false)
	}
	w.cache.forced[app] = time.Now()
	w.evictToken(key)
	entry.forcedAt = time.Now()
	entry.used = time.Now()
	w.cache.entries[key] = entry
	w.cache.mu.Unlock()
	token, ttl, err := w.tokenRequest(ctx, app, secret, true, 1)
	if err == nil {
		w.cache.mu.Lock()
		w.evictToken(key)
		entry = w.cache.entries[key]
		entry.token = token
		entry.expiry = time.Now().Add(ttl)
		w.cache.entries[key] = entry
		w.cache.mu.Unlock()
	}
	return token, err
}
