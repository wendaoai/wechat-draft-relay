package wechatdraft

import (
	"io"
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/gogf/gf/v2/net/ghttp"
)

// Service authentication is already checked by handle before these routes.
func (c *Controller) connectionRoute(r *ghttp.Request, requestID string) bool {
	check := r.URL.Path == prefix+"/service-check" && r.Method == http.MethodGet
	test := r.URL.Path == prefix+"/connection-test" && r.Method == http.MethodPost
	if !check && !test {
		return false
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	operation := "control"
	if check {
		operation = "query"
	}
	release, e := c.manager.Gate(operation, ip)
	if e != nil {
		c.respond(r, requestID, nil, e, 0)
		return true
	}
	defer release()
	if check {
		if !c.manager.Ready() {
			c.respond(r, requestID, nil, httpError(503, "EXECUTOR_UNAVAILABLE", "服务尚未就绪"), 0)
		} else {
			c.respond(r, requestID, map[string]bool{"ready": true}, nil, 200)
		}
		return true
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var input struct {
		AppID  string `json:"appid"`
		Secret string `json:"appsecret"`
	}
	body, readErr := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || contentType != "application/json" || readErr != nil || len(body) > 4096 || !StrictJSON(strings.NewReader(string(body)), &input) {
		c.respond(r, requestID, nil, httpError(422, "VALIDATION_FAILED", "连接测试需要有效的公众号凭据 JSON"), 0)
		return true
	}
	e = c.manager.TestConnection(r.Context(), input.AppID, input.Secret)
	c.respond(r, requestID, map[string]bool{"connected": e == nil}, e, 200)
	return true
}
