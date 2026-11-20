package wechatdraft

import (
	"context"
	"mime/multipart"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func (m *Manager) Begin(p v1.Payload, tokenHash string, retry bool) (*Upload, *v1.Task, *APIError) {
	k := key(p.AppID, p.UUID)
	unlock := m.locks.acquire(k)
	defer unlock()
	hash := RequestHash(p)
	st, e := m.read(p.AppID, p.UUID)
	if e == nil {
		if !sameHash(st.TaskTokenHash, tokenHash) {
			return nil, nil, notFound()
		}
		if st.ExpiresAt != nil && !m.config.now().Before(*st.ExpiresAt) {
			return nil, nil, fail(409, "REQUEST_EXPIRED", "任务已过期，请先核对公众号草稿箱")
		}
		if st.RequestHash != hash {
			return nil, nil, fail(409, "IDEMPOTENCY_CONFLICT", "这个 UUID 已用于不同内容，请使用新的 UUID")
		}
		if !retry {
			t := st.view()
			return nil, &t, nil
		}
		if st.Phase == "recovery_unknown" {
			return nil, nil, fail(409, "TASK_RECOVERY_REQUIRED", "任务证据不完整，请先核对公众号草稿箱")
		}
		if st.LastRetry != nil && st.LastRetry.RequestID == p.RetryRequestID {
			if st.LastRetry.ExpectedAttempt != p.ExpectedAttempt {
				return nil, nil, fail(409, "RETRY_OPERATION_CONFLICT", "重试操作的预期轮次不一致")
			}
			t := st.view()
			return nil, &t, nil
		}
		if st.Attempt != p.ExpectedAttempt {
			return nil, nil, fail(409, "ATTEMPT_CONFLICT", "任务轮次已变化，请重新读取状态")
		}
		if !retryAllowed(st) {
			return nil, nil, fail(409, "RETRY_NOT_ALLOWED", "当前任务不允许重试，请先核对公众号草稿箱")
		}
	} else {
		if retry {
			return nil, nil, notFound()
		}
		if m.existingDirectory(p.AppID, p.UUID) {
			gc := m.store.path(p.AppID, ".gc-"+p.UUID)
			if r, err := m.store.receipt(gc, p.AppID, p.UUID); err == nil && sameHash(r.TaskTokenHash, tokenHash) {
				return nil, nil, fail(409, "REQUEST_EXPIRED", "任务已过期，请先核对公众号草稿箱")
			}
			if e.Status == 503 {
				r, err := m.store.receipt(m.store.path(p.AppID, p.UUID), p.AppID, p.UUID)
				if err == nil && sameHash(r.TaskTokenHash, tokenHash) {
					return nil, nil, e
				}
			}
			return nil, nil, notFound()
		}
		if e.Status != 404 {
			return nil, nil, e
		}
		if err := creationWindow(p.UUID, m.config.now()); err != nil {
			return nil, nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if pending := m.pending[k]; pending != nil {
		if !sameHash(pending.tokenHash, tokenHash) {
			return nil, nil, notFound()
		}
		if pending.requestHash != hash {
			return nil, nil, fail(409, "IDEMPOTENCY_CONFLICT", "在途提交清单不同")
		}
		code := "CREATE_IN_PROGRESS"
		if retry {
			code = "RETRY_IN_PROGRESS"
		}
		err := fail(409, code, "本次上传尚未受理，请稍后查询")
		err.RetryAfter = 2
		return nil, nil, err
	}
	if p.AppSecret == "" {
		return nil, nil, fail(422, "VALIDATION_FAILED", "首次受理需要 appsecret")
	}
	if m.stopped || m.unready || m.ctx.Err() != nil {
		return nil, nil, m.stoppedError()
	}
	now := m.config.now()
	if !m.lastClock.IsZero() && now.Before(m.lastClock.Add(-time.Second)) {
		m.clockUnsafeUntil = m.lastClock
	}
	if now.Before(m.clockUnsafeUntil) {
		return nil, nil, fail(503, "CLOCK_UNAVAILABLE", "服务器时间异常，暂停新受理")
	}
	if now.After(m.lastClock) {
		m.lastClock = now
	}
	if !m.quota.allow("appwrite:"+p.AppID, 6, time.Minute, now) {
		return nil, nil, busy("WRITE_RATE_LIMIT")
	}
	if _, ok := m.active[p.AppID]; ok {
		return nil, nil, busy("APPID_BUSY")
	}
	if len(m.queue)+len(m.pending) >= m.config.QueueSize {
		return nil, nil, busy("QUEUE_FULL")
	}
	if len(m.pending) >= m.config.UploadConcurrency {
		return nil, nil, busy("UPLOAD_LIMIT")
	}
	if !m.store.budget.reserve(UploadReservation, m.config.DataDir) {
		return nil, nil, storageError()
	}
	ctx, cancel := context.WithTimeout(m.ctx, m.config.UploadTimeout)
	u := &Upload{manager: m, payload: p, tokenHash: tokenHash, requestHash: hash, stageID: stageID(), retry: retry, ctx: ctx, cancel: cancel, reserved: true}
	m.pending[k] = u
	// Keep the stage protected through rollback and accounting, even after
	// acceptance moves the task from pending to the executor.
	m.stages[u.stageID] = u
	m.active[p.AppID] = k
	m.wg.Add(1)
	return u, nil, nil
}

// Receive uses the upload context as well as the request cancellation. The
// executor's context is separate once this method durably accepts the task.
func (u *Upload) Receive(requestCtx context.Context, reader *multipart.Reader) (v1.Task, *APIError) {
	m := u.manager
	stop := context.AfterFunc(requestCtx, u.cancel)
	defer stop()
	defer u.Abort()
	dir, err := m.store.newStage(u.payload, u.stageID)
	u.dir = dir
	if err != nil {
		return v1.Task{}, storageError()
	}
	files, e := m.store.receive(u.ctx, dir, u.payload, reader)
	if e != nil {
		return v1.Task{}, e
	}
	if u.ctx.Err() != nil {
		return v1.Task{}, storageError()
	}
	k := key(u.payload.AppID, u.payload.UUID)
	unlock := m.locks.acquire(k)
	defer unlock()
	var st *State
	if u.retry {
		st, e = m.authenticated(u.payload.AppID, u.payload.UUID, u.tokenHash)
		if e != nil {
			return v1.Task{}, e
		}
		if st.Attempt != u.payload.ExpectedAttempt || !retryAllowed(st) || st.RequestHash != u.requestHash {
			return v1.Task{}, fail(409, "ATTEMPT_CONFLICT", "任务轮次已变化")
		}
		st = cloneState(st)
		st.Attempt++
		st.LastRetry = &v1.LastRetry{RequestID: u.payload.RetryRequestID, ExpectedAttempt: u.payload.ExpectedAttempt, AcceptedAttempt: st.Attempt}
		st.Status, st.Phase, st.Message = "queued", "accepted", "等待重新推送"
		st.Progress.Completed = 1
		st.Progress.Percent = 100 / st.Progress.Total
		st.CancelRequested = false
		st.Error = nil
		st.Result = nil
		st.FinishedAt = nil
		st.ExpiresAt = nil
		st.Materials = Materials{}
		st.UpdatedAt = m.config.now()
		st.QueuedAt = st.UpdatedAt
		if err = m.save(st); err != nil {
			m.retrySaveFailed(st)
			return v1.Task{}, storageError()
		}
	} else {
		if m.existingDirectory(u.payload.AppID, u.payload.UUID) {
			return v1.Task{}, fail(409, "CREATE_IN_PROGRESS", "任务目录已存在，请查询原任务")
		}
		st = initialState(u.payload, u.tokenHash, u.requestHash, m.config.now())
		if err = m.store.snapshot(dir, u.payload, st, files); err != nil {
			return v1.Task{}, storageError()
		}
		dest := m.store.path(u.payload.AppID, u.payload.UUID)
		err = m.store.commit(dir, dest)
		if _, statErr := os.Stat(dest); statErr == nil {
			u.dir = ""
			size, sizeErr := treeSize(dest)
			if sizeErr != nil {
				m.markUnready()
			}
			m.store.budget.settle(UploadReservation, size)
			u.reserved = false
		}
		if err != nil {
			if u.dir == "" {
				setFinished(st, "failed", &v1.Fault{Code: "STORAGE_UNAVAILABLE", Message: "任务提交同步失败，未执行微信请求"}, m.config.now())
				m.persistFinal(st)
			}
			return v1.Task{}, storageError()
		}
	}
	m.mu.Lock()
	if m.stopped || u.ctx.Err() != nil {
		m.mu.Unlock()
		setFinished(st, "failed", &v1.Fault{Code: "SERVER_RESTARTED", Message: "受理期间服务停止，请显式重试"}, m.config.now())
		m.persistFinal(st)
		return v1.Task{}, m.stoppedError()
	}
	j := &job{app: st.AppID, id: st.UUID, attempt: st.Attempt, secret: u.payload.AppSecret}
	m.jobs[k] = j
	m.queue = append(m.queue, j)
	delete(m.pending, k)
	m.mu.Unlock()
	m.events.accepted.Add(1)
	m.signal()
	return st.view(), nil
}

func (u *Upload) Abort() {
	u.once.Do(func() {
		m := u.manager
		u.cancel()
		actual := int64(0)
		if u.dir != "" {
			if _, err := m.store.remove(u.dir); err != nil {
				m.events.rollbackFailures.Add(1)
			}
			actual, _ = treeSize(u.dir)
		}
		if u.reserved {
			m.store.budget.settle(UploadReservation, actual)
		} else if actual > 0 {
			m.store.budget.adjust(actual)
		}
		m.mu.Lock()
		delete(m.stages, u.stageID)
		k := key(u.payload.AppID, u.payload.UUID)
		if m.pending[k] == u {
			delete(m.pending, k)
			if m.jobs[k] == nil && m.active[u.payload.AppID] == k {
				delete(m.active, u.payload.AppID)
			}
		}
		m.mu.Unlock()
		u.payload.AppSecret = ""
		m.wg.Done()
	})
}

func (u *Upload) Context() context.Context { return u.ctx }

func imagePath(dir, id string, r *Receipt) string {
	for _, f := range r.Files {
		if f.Name == "images/"+id+".jpg" || f.Name == "images/"+id+".png" || f.Name == "images/"+id+".gif" {
			return filepath.Join(dir, filepath.FromSlash(f.Name))
		}
	}
	return ""
}
