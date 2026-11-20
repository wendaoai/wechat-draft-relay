package wechatdraft

import "sync"

type DiskBudget struct {
	mu                    sync.Mutex
	Used, Reserved, Limit int64
}

func (b *DiskBudget) reserve(n int64, root string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.canReserve(n, root) {
		return false
	}
	b.Reserved += n
	return true
}

func (b *DiskBudget) available(n int64, root string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.canReserve(n, root)
}

// Caller holds mu. Probe without consuming upload capacity for readiness.
func (b *DiskBudget) canReserve(n int64, root string) bool {
	free, err := diskFree(root)
	return err == nil && free >= b.Reserved+n+256*MiB && b.Used+b.Reserved+n+StateReserve <= b.Limit
}
func (b *DiskBudget) settle(reserved, actual int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Reserved -= reserved
	b.Used += actual
}
func (b *DiskBudget) adjust(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Used += n
}
func (b *DiskBudget) snapshot() (int64, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Used, b.Reserved
}
