package wechatdraft

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/gogf/gf/v2/net/ghttp"
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

func (c *Controller) upload(r *ghttp.Request, requestID, app, id, tokenHash string, retry bool, release func()) {
	// Control slots are held only while reading the bounded first part and
	// deciding admission. They are released before any file is received.
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	if r.ContentLength > logic.MaxRequest {
		c.respond(r, requestID, nil, httpError(413, "PAYLOAD_TOO_LARGE", "请求大小超限"), 0)
		return
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" || len(params["boundary"]) > 70 || params["boundary"] == "" {
		c.respond(r, requestID, nil, httpError(422, "VALIDATION_FAILED", "需要 multipart/form-data"), 0)
		return
	}
	bounded := &controlReader{reader: r.Body, remaining: logic.MaxRequest}
	reader := multipart.NewReader(bounded, params["boundary"])
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "payload" || part.FileName() != "" {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.respond(r, requestID, nil, httpError(413, "PAYLOAD_TOO_LARGE", "控制 part 大小超限"), 0)
			return
		}
		c.respond(r, requestID, nil, httpError(422, "VALIDATION_FAILED", "第一 part 必须是 payload 文本"), 0)
		return
	}
	b, err := io.ReadAll(io.LimitReader(part, logic.MaxRequest+1))
	var controlTooLarge *http.MaxBytesError
	if errors.As(err, &controlTooLarge) {
		c.respond(r, requestID, nil, httpError(413, "PAYLOAD_TOO_LARGE", "控制 part 大小超限"), 0)
		return
	}
	if len(b) > int(logic.MaxRequest) {
		c.respond(r, requestID, nil, httpError(413, "PAYLOAD_TOO_LARGE", "请求大小超限"), 0)
		return
	}
	var p v1.Payload
	if err != nil || !utf8.Valid(b) || !StrictJSON(bytes.NewReader(b), &p) {
		c.respond(r, requestID, nil, httpError(422, "VALIDATION_FAILED", "payload JSON 无效"), 0)
		return
	}
	if e := logic.ValidatePayload(&p, retry); e != nil {
		c.respond(r, requestID, nil, e, 0)
		return
	}
	if p.AppID != app || p.UUID != id {
		c.respond(r, requestID, nil, httpError(422, "VALIDATION_FAILED", "路径、Header 与 payload 身份必须一致"), 0)
		return
	}
	u, replay, e := c.manager.Begin(p, tokenHash, retry)
	p.AppSecret = ""
	release()
	released = true
	if e != nil {
		c.respond(r, requestID, nil, e, 0)
		return
	}
	if replay != nil {
		c.respond(r, requestID, replay, nil, 200)
		return
	}
	defer u.Abort()
	bounded.admitted = true
	stop := context.AfterFunc(u.Context(), func() { r.Body.Close() })
	defer stop()
	setReadDeadline(r, 120*time.Second)
	task, e := u.Receive(r.Context(), reader)
	c.respond(r, requestID, task, e, 202)
}

func (c *Controller) cancel(r *ghttp.Request, requestID, app, id, tokenHash string) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		c.respond(r, requestID, nil, httpError(422, "VALIDATION_FAILED", "取消需要 JSON"), 0)
		return
	}
	var p struct {
		ExpectedAttempt int `json:"expectedAttempt"`
	}
	body, readErr := io.ReadAll(io.LimitReader(r.Body, 1025))
	if readErr != nil || len(body) > 1024 || !StrictJSON(bytes.NewReader(body), &p) || p.ExpectedAttempt < 1 {
		c.respond(r, requestID, nil, httpError(422, "VALIDATION_FAILED", "需要 expectedAttempt"), 0)
		return
	}
	task, e := c.manager.Cancel(app, id, tokenHash, p.ExpectedAttempt)
	c.respond(r, requestID, task, e, 200)
}
