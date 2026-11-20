package wechatdraft

import (
	"context"
	"time"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		if m.ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		var j *job
		if len(m.queue) > 0 {
			j = m.queue[0]
			m.queue = m.queue[1:]
			m.signal()
		}
		m.mu.Unlock()
		if j == nil {
			select {
			case <-m.ctx.Done():
				return
			case <-m.notify:
				continue
			}
		}
		m.execute(j)
	}
}
func (m *Manager) execute(j *job) {
	defer m.release(j)
	k := key(j.app, j.id)
	unlock := m.locks.acquire(k)
	st, e := m.read(j.app, j.id)
	if e != nil || st.Attempt != j.attempt || st.Status != "queued" {
		unlock()
		return
	}
	if m.config.now().Sub(st.QueuedAt) >= m.config.QueueTimeout {
		setFinished(st, "failed", &v1.Fault{Code: "QUEUE_TIMEOUT", Message: "排队超时，请稍后显式重试"}, m.config.now())
		m.persistFinal(st)
		unlock()
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, m.config.TaskTimeout)
	defer cancel()
	m.mu.Lock()
	j.cancel = cancel
	j.running = true
	m.mu.Unlock()
	st.Status = "running"
	st.Phase = "getting_token"
	st.Message = "正在获取微信 Token"
	st.UpdatedAt = m.config.now()
	if err := m.save(st); err != nil {
		unlock()
		m.finish(j, "failed", &v1.Fault{Code: "STORAGE_UNAVAILABLE", Message: "无法保存任务阶段"}, "")
		return
	}
	m.mu.Lock()
	m.queueWaits[m.queueWaitCount%len(m.queueWaits)] = float64(m.config.now().Sub(st.QueuedAt).Microseconds()) / 1000
	m.queueWaitCount++
	m.mu.Unlock()
	unlock()
	m.run(ctx, j)
}

func (m *Manager) update(j *job, phase, message string, completed int, mutate func(*State)) bool {
	unlock := m.locks.acquire(key(j.app, j.id))
	defer unlock()
	st, e := m.read(j.app, j.id)
	if e != nil || st.Attempt != j.attempt || st.Status != "running" || st.CancelRequested {
		return false
	}
	st = cloneState(st)
	st.Phase, st.Message, st.UpdatedAt = phase, message, m.config.now()
	st.Progress.Completed = completed
	st.Progress.Percent = completed * 100 / st.Progress.Total
	if mutate != nil {
		mutate(st)
	}
	return m.save(st) == nil
}
func (m *Manager) run(ctx context.Context, j *job) {
	dir := m.store.path(j.app, j.id)
	r, err := m.store.receipt(dir, j.app, j.id)
	if err != nil {
		m.failLocal(j, ctx)
		return
	}
	if err = m.store.validateInput(dir, r); err != nil {
		m.finish(j, "uncertain", &v1.Fault{Code: "STATE_CORRUPTED", Message: "输入快照损坏，请先核对公众号草稿箱"}, "")
		return
	}
	p, err := m.store.loadInput(dir)
	if err != nil {
		m.failLocal(j, ctx)
		return
	}
	token, we := m.wechat.Token(ctx, j.app, j.secret)
	if we != nil {
		m.failWechat(j, ctx, we)
		return
	}
	if !m.update(j, "uploading_cover", "正在上传永久封面", 2, nil) {
		m.failLocal(j, ctx)
		return
	}
	cover, token, we := m.wechat.materialFor(ctx, j.app, j.secret, token, imagePath(dir, p.CoverImageID, r), true)
	if we != nil {
		m.failWechat(j, ctx, we)
		return
	}
	if !m.update(j, "uploading_body_images", "正在上传正文图片", 3, func(s *State) { s.Materials.CoverMediaID = cover }) {
		m.failLocal(j, ctx)
		return
	}
	urls := map[string]string{}
	completed := 3
	for _, im := range p.Images {
		if im.Role != "body" {
			continue
		}
		var value string
		value, token, we = m.wechat.materialFor(ctx, j.app, j.secret, token, imagePath(dir, im.ID, r), p.Article.ArticleType == "newspic")
		if we != nil {
			m.failWechat(j, ctx, we)
			return
		}
		urls[im.ID] = value
		completed++
		if !m.update(j, "uploading_body_images", "正文图片已上传", completed, func(s *State) {
			s.Materials.BodyImages = append(s.Materials.BodyImages, BodyMaterial{ImageID: im.ID, SHA256: im.SHA256, URL: value})
		}) {
			m.failLocal(j, ctx)
			return
		}
	}
	if !m.update(j, "preparing_content", "正在准备微信正文", completed, nil) {
		m.failLocal(j, ctx)
		return
	}
	content := p.Article.Digest
	gallery := []string{}
	if p.Article.ArticleType == "newspic" {
		for _, id := range p.Article.ImageIDs {
			gallery = append(gallery, urls[id])
		}
	} else {
		var e *APIError
		content, e = ProcessHTML(p.Article.ContentHTML, p.Images, urls)
		if e != nil {
			m.finish(j, "failed", &e.Fault, "")
			return
		}
	}
	if err = m.store.writeFile(dir, "wechat.html", []byte(content), true); err != nil {
		m.failLocal(j, ctx)
		return
	}
	if ctx.Err() != nil {
		m.failLocal(j, ctx)
		return
	}
	// update takes the same lock as cancel, atomically checking cancellation
	// and durably recording the point beyond which cancellation is unsafe.
	if !m.update(j, "submitting_draft", "正在创建公众号草稿", completed, nil) {
		m.failLocal(j, ctx)
		return
	}
	media, we := m.wechat.draftFor(ctx, j.app, j.secret, token, p.Article, content, cover, gallery...)
	if we != nil {
		m.failWechat(j, ctx, we)
		return
	}
	m.finish(j, "succeeded", nil, media)
}

func (m *Manager) failLocal(j *job, ctx context.Context) {
	code, message := "STORAGE_UNAVAILABLE", "无法保存任务文件或进度"
	if ctx.Err() != nil {
		code, message = "TASK_TIMEOUT", "任务超时或服务正在停止"
	}
	m.finish(j, "failed", &v1.Fault{Code: code, Message: message}, "")
}
func (m *Manager) failWechat(j *job, ctx context.Context, e *WechatError) {
	status := "failed"
	if e.Uncertain {
		status = "uncertain"
		e.Fault.Code = "WECHAT_RESULT_UNKNOWN"
		e.Fault.Message = "微信提交结果未知，请先检查公众号草稿箱"
	}
	if ctx.Err() != nil && status != "uncertain" {
		m.failLocal(j, ctx)
		return
	}
	m.finish(j, status, &e.Fault, "")
}
func (m *Manager) finish(j *job, status string, fault *v1.Fault, media string) {
	unlock := m.locks.acquire(key(j.app, j.id))
	defer unlock()
	st, e := m.read(j.app, j.id)
	if e != nil || st.Attempt != j.attempt || terminal(st) {
		return
	}
	if st.CancelRequested && st.Phase != "submitting_draft" {
		status = "cancelled"
		fault = &v1.Fault{Code: "TASK_CANCELLED", Message: "已取消推送"}
	}
	if st.Phase == "submitting_draft" && status == "failed" && (fault == nil || fault.Code == "TASK_TIMEOUT" || fault.Code == "STORAGE_UNAVAILABLE") {
		status = "uncertain"
		fault = &v1.Fault{Code: "WECHAT_RESULT_UNKNOWN", Message: "提交结果未知，请先检查公众号草稿箱"}
	}
	setFinished(st, status, fault, m.config.now())
	if status == "succeeded" && media != "" {
		st.Phase = "done"
		st.Message = "已推送到公众号草稿箱"
		st.Result = &v1.Result{MediaID: media, CompletedAt: *st.FinishedAt}
		st.Progress.Completed = st.Progress.Total
		st.Progress.Percent = 100
	}
	m.persistFinal(st)
}
func (m *Manager) persistFinal(st *State) {
	for n := 0; n < 3; n++ {
		if m.save(st) == nil {
			m.recordTerminal(st.Status)
			return
		}
		if n < 2 {
			timer := time.NewTimer(time.Duration(n+1) * 50 * time.Millisecond)
			select {
			case <-m.ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	// Keep the actual receipt in memory, but never report success before disk.
	fallback := cloneState(st)
	fallback.Status = "uncertain"
	fallback.Message = "本地回执保存失败，请先检查公众号草稿箱"
	fallback.Error = &v1.Fault{Code: "STORAGE_UNAVAILABLE", Message: fallback.Message}
	fallback.Result = nil
	if st.Status == "succeeded" {
		fallback.Phase = "submitting_draft"
	}
	if fallback.Progress.Percent == 100 {
		fallback.Progress.Completed--
		fallback.Progress.Percent = fallback.Progress.Completed * 100 / fallback.Progress.Total
	}
	m.mu.Lock()
	m.overrides[key(st.AppID, st.UUID)] = fallback
	m.unready = true
	m.mu.Unlock()
	m.recordTerminal("uncertain")
}
