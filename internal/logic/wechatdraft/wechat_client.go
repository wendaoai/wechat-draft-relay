package wechatdraft

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

const newspicDigestLimit = 120

type WechatError struct {
	Fault                              v1.Fault
	Uncertain, Temporary, TokenInvalid bool
}

func (e *WechatError) Error() string { return e.Fault.Code }

type Wechat struct {
	baseURL string
	http    *http.Client
	cache   tokenCache

	// Test-only delay override; production uses bounded jittered backoff.
	backoff func(context.Context, int) error
}

func NewWechat(workers int) *Wechat {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConns: workers * 4, MaxIdleConnsPerHost: workers * 2, MaxConnsPerHost: workers * 2,
		IdleConnTimeout: 90 * time.Second, ExpectContinueTimeout: time.Second}
	return newWechat("https://api.weixin.qq.com", &http.Client{Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
}
func newWechat(base string, client *http.Client) *Wechat {
	return &Wechat{baseURL: base, http: client, cache: tokenCache{entries: map[string]tokenEntry{}, loading: map[string]chan struct{}{}}, backoff: func(ctx context.Context, n int) error {
		timer := time.NewTimer(time.Duration(n)*time.Second + time.Duration(rand.IntN(250))*time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}}
}
func (w *Wechat) Close() { w.http.CloseIdleConnections() }

func (w *Wechat) call(ctx context.Context, path string, query url.Values, body io.Reader, contentType string, timeout time.Duration, draft bool) (result map[string]json.RawMessage, failure *WechatError) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	u := w.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return nil, wxUnavailable(draft)
	}
	req.Header.Set("Content-Type", contentType)
	if sized, ok := body.(interface{ ContentLength() int64 }); ok {
		req.ContentLength = sized.ContentLength()
	}
	res, err := w.http.Do(req)
	if err != nil {
		return nil, wxTransportFailure(path, err, draft)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, MiB+1))

	if res.StatusCode != http.StatusOK {
		return nil, wxFailure(path, "http_status", res.StatusCode, draft)
	}
	if err != nil {
		return nil, wxFailure(path, "response_read", res.StatusCode, draft)
	}
	if len(b) > int(MiB) {
		return nil, wxFailure(path, "response_too_large", res.StatusCode, draft)
	}
	obj, valid := responseObject(b)
	if !valid {
		return nil, wxFailure(path, "invalid_json", res.StatusCode, draft)
	}
	if raw, ok := obj["errcode"]; ok {
		var code int
		if string(raw) == "null" || json.Unmarshal(raw, &code) != nil {
			return nil, wxFailure(path, "invalid_errcode", res.StatusCode, draft)
		}
		if code != 0 {
			failure := wxCode(code, draft)
			failure.Fault.Details = wechatFailureDetails{Stage: wechatStage(path)}
			return nil, failure
		}
	}
	return obj, nil
}
func rawString(obj map[string]json.RawMessage, key string) string {
	var v string
	if json.Unmarshal(obj[key], &v) != nil || len(v) > 2048 || strings.ContainsAny(v, "\x00\r\n") {
		return ""
	}
	return v
}
func wxUnavailable(draft bool) *WechatError {
	return &WechatError{Fault: v1.Fault{Code: "WECHAT_UNAVAILABLE", Message: "微信接口暂不可用"}, Uncertain: draft, Temporary: true}
}
func wxCode(code int, draft bool) *WechatError {
	e := &WechatError{Fault: v1.Fault{WechatErrcode: code}}
	switch code {
	case 40001, 40014, 42001:
		e.Fault.Code = "WECHAT_TOKEN_INVALID"
		e.Fault.Message = "微信 Token 失效"
		e.TokenInvalid = true
	case 40013, 40125:
		e.Fault.Code = "WECHAT_CREDENTIAL_INVALID"
		e.Fault.Message = "公众号凭据无效"
	case 40164:
		e.Fault.Code = "WECHAT_IP_NOT_ALLOWED"
		e.Fault.Message = "服务器出口 IP 未加入公众号白名单"
	case 48001, 48002, 48004, 50001:
		e.Fault.Code = "WECHAT_PERMISSION_DENIED"
		e.Fault.Message = "公众号没有所需接口权限"
	case 45009, 45011:
		e.Fault.Code, e.Fault.Message = "WECHAT_QUOTA_EXCEEDED", "微信接口调用额度或频率超限"
	case 89503:
		e.Fault.Code, e.Fault.Message = "WECHAT_ADMIN_CONFIRMATION", "请公众号管理员完成微信确认后再试"
	case 89506, 89507:
		e.Fault.Code, e.Fault.Message = "WECHAT_ADMIN_DENIED", "公众号管理员未允许本次连接"
	case 40002, 40003, 40004, 40005, 40006, 40007, 40008, 40009, 40035, 41001, 41002, 41004, 41005, 44001, 44002, 45001, 45002, 45003, 45004, 45005, 45006, 45007, 45008, 45010, 45028, 45166:
		e.Fault.Code = "WECHAT_CONTENT_REJECTED"
		e.Fault.Message = "微信拒绝文章或素材参数"
	case -1:
		e = wxUnavailable(draft)
		e.Fault.WechatErrcode = code
		e.Fault.Message = "微信服务繁忙，请稍后重试"
	default:
		e.Fault.Code, e.Fault.Message = "WECHAT_REJECTED", "微信接口拒绝了请求"
	}
	return e
}
func jsonReader(v any) io.Reader { b, _ := json.Marshal(v); return bytes.NewReader(b) }

func encodeCoverInfo(info *v1.CoverInfo) map[string]any {
	if info == nil || len(info.CropPercentList) == 0 {
		return nil
	}
	crops := make([]map[string]string, 0, len(info.CropPercentList))
	for _, cp := range info.CropPercentList {
		crops = append(crops, map[string]string{
			"ratio": cp.Ratio,
			"x1":    cp.X1,
			"y1":    cp.Y1,
			"x2":    cp.X2,
			"y2":    cp.Y2,
		})
	}
	return map[string]any{"crop_percent_list": crops}
}

func (w *Wechat) Draft(ctx context.Context, token string, a v1.Article, content, cover string, gallery ...string) (string, *WechatError) {
	var article map[string]any
	if a.ArticleType == "newspic" {
		images := make([]map[string]string, 0, len(gallery))
		for _, id := range gallery {
			if !validMediaID(id) {
				return "", wxFailure("/cgi-bin/draft/add", "missing_media_id", 400, false)
			}
			images = append(images, map[string]string{"image_media_id": id})
		}
		if len(images) < 1 || len(images) > 20 {
			return "", wxFailure("/cgi-bin/draft/add", "image_count", 400, false)
		}
		article = map[string]any{
			"article_type":          "newspic",
			"title":                 a.Title,
			"content":               content,
			"image_info":            map[string]any{"image_list": images},
			"need_open_comment":     a.NeedOpenComment,
			"only_fans_can_comment": a.OnlyFansCanComment,
		}
		if a.Author != "" {
			article["author"] = a.Author
		}
		if a.Digest != "" && utf8.RuneCountInString(a.Digest) <= newspicDigestLimit {
			article["digest"] = a.Digest
		}
		if a.ContentSourceURL != "" {
			article["content_source_url"] = a.ContentSourceURL
		}
	} else {
		article = map[string]any{
			"article_type":          "news",
			"title":                 a.Title,
			"author":                a.Author,
			"digest":                a.Digest,
			"content":               content,
			"thumb_media_id":        cover,
			"content_source_url":    a.ContentSourceURL,
			"need_open_comment":     a.NeedOpenComment,
			"only_fans_can_comment": a.OnlyFansCanComment,
		}
	}
	if coverInfo := encodeCoverInfo(a.CoverInfo); coverInfo != nil {
		article["cover_info"] = coverInfo
	}
	obj, err := w.call(ctx, "/cgi-bin/draft/add", url.Values{"access_token": {token}}, jsonReader(map[string]any{"articles": []any{article}}), "application/json", 60*time.Second, true)
	if err != nil {
		return "", err
	}
	id := rawString(obj, "media_id")
	if !validMediaID(id) {
		return "", wxFailure("/cgi-bin/draft/add", "missing_media_id", http.StatusOK, true)
	}
	return id, nil
}
func (w *Wechat) tokenRequest(ctx context.Context, app, secret string, force bool, attempts int) (string, time.Duration, *WechatError) {
	var last *WechatError
	for n := 1; n <= attempts; n++ {
		obj, err := w.call(ctx, "/cgi-bin/stable_token", nil, jsonReader(map[string]any{"grant_type": "client_credential", "appid": app, "secret": secret, "force_refresh": force}), "application/json", 15*time.Second, false)
		if err == nil {
			var expiry int
			if json.Unmarshal(obj["expires_in"], &expiry) == nil && expiry > 0 && expiry <= 86400 && rawString(obj, "access_token") != "" {
				return rawString(obj, "access_token"), time.Duration(expiry-300) * time.Second, nil
			}
			err = wxFailure("/cgi-bin/stable_token", "invalid_token", http.StatusOK, false)
		}
		last = err
		if !err.Temporary || n == attempts {
			break
		}
		if err := w.backoff(ctx, n); err != nil {
			break
		}
	}
	if last == nil {
		last = wxUnavailable(false)
	}
	return "", 0, last
}

// NewLocalWechat supports isolated HTTP acceptance tests. Production startup
// always uses NewWechat; this constructor refuses non-loopback dependencies.
func NewLocalWechat(endpoint string) *Wechat {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		panic("local Wechat dependency must use loopback HTTP")
	}
	client := &http.Client{Transport: &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 128, IdleConnTimeout: time.Minute}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	w := newWechat(endpoint, client)
	w.backoff = func(ctx context.Context, n int) error {
		timer := time.NewTimer(time.Duration(n) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	return w
}
