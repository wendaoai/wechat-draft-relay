package wechatdraft

import (
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func (m *Manager) Cancel(app, id, tokenHash string, expected int) (v1.Task, *APIError) {
	k := key(app, id)
	unlock := m.locks.acquire(k)
	defer unlock()
	st, e := m.authenticated(app, id, tokenHash)
	if e != nil {
		return v1.Task{}, e
	}
	if e = m.TaskQuota(app, id, "cancel"); e != nil {
		return v1.Task{}, e
	}
	if expected != st.Attempt {
		return v1.Task{}, fail(409, "ATTEMPT_CONFLICT", "任务轮次已变化")
	}
	if st.Status == "succeeded" || st.Status == "uncertain" || st.Phase == "submitting_draft" {
		return v1.Task{}, fail(409, "CANCEL_TOO_LATE", "已开始提交，请查询实际结果")
	}
	if terminal(st) {
		return st.view(), nil
	}
	st.CancelRequested = true
	st.UpdatedAt = m.config.now()
	st.Message = "正在取消推送"
	if st.Status == "queued" {
		setFinished(st, "cancelled", &v1.Fault{Code: "TASK_CANCELLED", Message: "已取消推送"}, m.config.now())
	}
	if err := m.save(st); err != nil {
		return v1.Task{}, storageError()
	}
	m.mu.Lock()
	j := m.jobs[k]
	var cancel func()
	if j != nil && j.attempt == expected {
		cancel = j.cancel
	}
	m.mu.Unlock()
	if st.Status == "cancelled" && j != nil {
		m.recordTerminal("cancelled")
		m.release(j)
	} else if cancel != nil {
		cancel()
	}
	return st.view(), nil
}
