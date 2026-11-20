package wechatdraft

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/google/uuid"
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

const prefix = "/api/v1/wechat-drafts"

type Controller struct {
	manager     *logic.Manager
	serviceHash [32]byte
}

type readDeadlineKey struct{}

func setReadDeadline(r *ghttp.Request, timeout time.Duration) {
	if controller, ok := r.Context().Value(readDeadlineKey{}).(*http.ResponseController); ok {
		_ = controller.SetReadDeadline(time.Now().Add(timeout))
	}
}

func Bind(server *ghttp.Server, manager *logic.Manager, serviceKey string) {
	controller := &Controller{manager: manager, serviceHash: sha256.Sum256([]byte(serviceKey))}
	server.BindHandler("/*", controller.handle)
}
func (c *Controller) handle(r *ghttp.Request) {
	requestID := uuid.NewString()
	r.Response.Header().Set("X-Request-ID", requestID)
	r.Response.Header().Set("Cache-Control", "no-store")
	r.Response.Header().Set("X-Content-Type-Options", "nosniff")
	path := r.URL.Path
	if r.Method == http.MethodGet && (path == "/healthz" || path == "/readyz") {
		status, message := 200, "ok"
		if path == "/readyz" && !c.manager.Ready() {
			status, message = 503, "unavailable"
		}
		r.Response.WriteHeader(status)
		r.Response.WriteJson(map[string]string{"status": message})
		return
	}
	if r.URL.RawQuery != "" {
		c.respond(r, requestID, nil, httpError(422, "VALIDATION_FAILED", "接口不接受 URL 查询参数"), 0)
		return
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	actual := sha256.Sum256([]byte(provided))
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(actual[:], c.serviceHash[:]) != 1 {
		c.respond(r, requestID, nil, httpError(401, "SERVICE_AUTH_REQUIRED", "需要服务访问密钥"), 0)
		return
	}
	if c.connectionRoute(r, requestID) {
		return
	}
	if path != prefix && !strings.HasPrefix(path, prefix+"/") {
		c.respond(r, requestID, nil, httpError(404, "TASK_NOT_FOUND", "接口不存在"), 0)
		return
	}
	app, id, e := logic.Identity(r.Header.Get("X-Wechat-AppId"), r.Header.Get("X-Draft-UUID"))
	if r.Method == http.MethodGet {
		pieces := strings.Split(strings.TrimPrefix(path, prefix+"/"), "/")
		if strings.HasPrefix(path, prefix+"/") && len(pieces) == 2 {
			app, id, e = logic.Identity(pieces[0], pieces[1])
		}
	}
	if e != nil {
		c.respond(r, requestID, nil, e, 0)
		return
	}
	tokenHash, e := logic.TokenHash(r.Header.Get("X-Draft-Token"))
	if e != nil {
		c.respond(r, requestID, nil, e, 0)
		return
	}
	operation := "control"
	if r.Method == http.MethodGet {
		operation = "query"
	} else if strings.HasSuffix(path, "/cancel") {
		operation = "cancel"
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	release, e := c.manager.Gate(operation, ip)
	if e != nil {
		c.respond(r, requestID, nil, e, 0)
		return
	}
	if r.Method == http.MethodPost && path == prefix {
		c.upload(r, requestID, app, id, tokenHash, false, release)
		return
	}
	if path != prefix+"/"+app+"/"+id && path != prefix+"/"+app+"/"+id+"/retry" && path != prefix+"/"+app+"/"+id+"/cancel" {
		release()
		c.respond(r, requestID, nil, httpError(404, "TASK_NOT_FOUND", "任务不存在"), 0)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/retry") {
		c.upload(r, requestID, app, id, tokenHash, true, release)
		return
	}
	defer release()
	if r.Method == http.MethodGet && path == prefix+"/"+app+"/"+id {
		task, e := c.manager.Get(app, id, tokenHash)
		if e == nil {
			e = c.manager.TaskQuota(app, id, "query")
		}
		c.respond(r, requestID, task, e, 200)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/cancel") {
		c.cancel(r, requestID, app, id, tokenHash)
		return
	}
	c.respond(r, requestID, nil, httpError(404, "TASK_NOT_FOUND", "接口不存在"), 0)
}

func (c *Controller) respond(r *ghttp.Request, requestID string, data any, e *logic.APIError, status int) {
	envelope := v1.Envelope{RequestID: requestID}
	if e != nil {
		status = e.Status
		envelope.Error = &e.Fault
		if e.RetryAfter > 0 {
			r.Response.Header().Set("Retry-After", jsonNumber(e.RetryAfter))
		}
	} else {
		envelope.Data = data
	}
	if r.URL.Path == "/api/v1/wechat-revenue/query" {
		code := "OK"
		if e != nil {
			code = e.Fault.Code
		}
		log.Printf("wechat_revenue_response at=%s request_id=%s status=%d code=%s", time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format(time.RFC3339Nano), requestID, status, code)
	}
	r.Response.WriteHeader(status)
	r.Response.WriteJson(envelope)
}
func httpError(status int, code, message string) *logic.APIError {
	e := &logic.APIError{Status: status, Fault: v1.Fault{Code: code, Message: message}}
	if code == "PAYLOAD_TOO_LARGE" {
		e.Fault.Details = map[string]string{"reason": "request_limit"}
	}
	return e
}
func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }

// StrictJSON never returns decoder error text, which could quote credentials.
func StrictJSON(reader io.Reader, target any) bool {
	d := json.NewDecoder(reader)
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return false
	}
	var extra any
	return d.Decode(&extra) == io.EOF
}

func Configure(server *ghttp.Server, c logic.Config) error {
	config := ghttp.NewConfig()
	config.Address = c.Listen
	// GoFrame does not expose ReadHeaderTimeout. A five-second read timeout
	// also bounds headers; admitted uploads extend the body deadline explicitly.
	config.ReadTimeout = 5 * time.Second
	config.WriteTimeout = 130 * time.Second
	config.IdleTimeout = 60 * time.Second
	config.MaxHeaderBytes = 8192
	config.ClientMaxBodySize = logic.MaxRequest
	config.KeepAlive = true
	config.AccessLogEnabled = false
	config.ErrorLogEnabled = false
	config.DumpRouterMap = false
	config.FileServerEnabled = false
	config.Graceful = false
	config.GracefulShutdownTimeout = 30
	config.Handler = func(w http.ResponseWriter, r *http.Request) {
		// Early exits do not drain arbitrary replay files to keep connections alive.
		if r.Method == http.MethodPost {
			w.Header().Set("Connection", "close")
			r.Close = true
		}
		r.Body = http.MaxBytesReader(w, r.Body, logic.MaxRequest)
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
		r = r.WithContext(context.WithValue(r.Context(), readDeadlineKey{}, controller))
		server.ServeHTTP(w, r)
	}
	return server.SetConfig(config)
}
