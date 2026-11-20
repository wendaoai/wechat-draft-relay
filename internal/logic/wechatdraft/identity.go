package wechatdraft

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

var appPattern = regexp.MustCompile(`^wx[0-9a-f]{16}$`)
var imagePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type APIError struct {
	Status     int
	Fault      v1.Fault
	RetryAfter int
}

func (e *APIError) Error() string { return e.Fault.Code }
func fail(status int, code, message string) *APIError {
	e := &APIError{Status: status, Fault: v1.Fault{Code: code, Message: message}}
	if code == "PAYLOAD_TOO_LARGE" {
		e.Fault.Details = map[string]string{"reason": "request_limit"}
	}
	return e
}
func busy(code string) *APIError {
	e := fail(429, code, "服务繁忙，请稍后重试")
	e.RetryAfter = 5
	return e
}
func storageError() *APIError { return fail(503, "STORAGE_UNAVAILABLE", "存储暂不可用") }
func notFound() *APIError     { return fail(404, "TASK_NOT_FOUND", "任务不存在或已过期") }

func Identity(appid, id string) (string, string, *APIError) {
	appid, id = strings.ToLower(appid), strings.ToLower(id)
	u, err := uuid.Parse(id)
	if !appPattern.MatchString(appid) || err != nil || u.Version() != 7 || u.Variant() != uuid.RFC4122 || u.String() != id {
		return "", "", fail(422, "VALIDATION_FAILED", "appid 或任务 UUID v7 格式无效")
	}
	return appid, id, nil
}
func OperationID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.Version() == 4 && u.Variant() == uuid.RFC4122 && u.String() == strings.ToLower(id)
}
func TokenHash(token string) (string, *APIError) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != token {
		return "", notFound()
	}
	return digest(b), nil
}
func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func sameHash(a, b string) bool {
	return len(a) == 64 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func RequestHash(p v1.Payload) string {
	p.AppSecret, p.RetryRequestID, p.ExpectedAttempt = "", "", 0
	p.Images = append([]v1.Image(nil), p.Images...)
	sort.Slice(p.Images, func(i, j int) bool { return p.Images[i].ID < p.Images[j].ID })
	b, _ := json.Marshal(p)
	return digest(b)
}
func uuidTime(id string) time.Time {
	u := uuid.MustParse(id)
	var ms int64
	for _, b := range u[:6] {
		ms = ms<<8 | int64(b)
	}
	return time.UnixMilli(ms)
}
func creationWindow(id string, now time.Time) *APIError {
	t := uuidTime(id)
	if t.Before(now.Add(-5 * time.Minute)) {
		e := fail(409, "REQUEST_EXPIRED", "提交标识已过期，请先检查公众号草稿箱")
		e.Fault.Details = map[string]any{"serverTime": now}
		return e
	}
	if t.After(now.Add(30 * time.Second)) {
		e := fail(422, "UUID_TIME_INVALID", "提交标识时间超前，请校准时间")
		e.Fault.Details = map[string]any{"serverTime": now}
		return e
	}
	return nil
}
