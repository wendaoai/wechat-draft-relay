package wechatdraft

import v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"

// Caller holds the task lock, and this retry has not entered the executor.
// Atomic replacement can become visible before directory sync fails. Preserve
// the visible operation ID, but never leave it queued without an execution job.
func (m *Manager) retrySaveFailed(next *State) {
	saved, e := m.read(next.AppID, next.UUID)
	if e != nil {
		// The state cannot be inspected safely. Expose uncertainty and stop new
		// acceptance rather than assume the failed replacement did not happen.
		setFinished(next, "uncertain", &v1.Fault{Code: "STORAGE_UNAVAILABLE", Message: "重试状态无法确认，请修复存储后查询任务"}, m.config.now())
		m.markUnready()
		m.persistFinal(next)
		return
	}
	if saved.Phase == "recovery_unknown" {
		m.markUnready()
		return
	}
	if saved.Attempt != next.Attempt || saved.Status != "queued" || saved.LastRetry == nil || saved.LastRetry.RequestID != next.LastRetry.RequestID {
		return // The old attempt survived the replacement; leave it intact.
	}
	setFinished(saved, "failed", &v1.Fault{Code: "STORAGE_UNAVAILABLE", Message: "重试状态同步失败，未执行微信请求"}, m.config.now())
	m.persistFinal(saved)
}
