package wechatdraft

import (
	"container/list"
	"sync"
)

type receiptEntry struct {
	key   string
	value *Receipt
}
type receiptCache struct {
	mu      sync.Mutex
	lru     *list.List
	entries map[string]*list.Element
}

func (c *receiptCache) get(key string) *Receipt {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		c.lru.MoveToFront(e)
		return e.Value.(receiptEntry).value
	}
	return nil
}
func (c *receiptCache) put(key string, r *Receipt) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*list.Element{}
		c.lru = list.New()
	}
	if e := c.entries[key]; e != nil {
		c.lru.MoveToFront(e)
		return
	}
	c.entries[key] = c.lru.PushFront(receiptEntry{key, r})
	if c.lru.Len() > 256 {
		e := c.lru.Back()
		delete(c.entries, e.Value.(receiptEntry).key)
		c.lru.Remove(e)
	}
}
func (c *receiptCache) remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		delete(c.entries, key)
		c.lru.Remove(e)
	}
}
