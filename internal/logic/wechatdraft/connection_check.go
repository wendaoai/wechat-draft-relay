package wechatdraft

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

// TestConnection only acquires a token from this server's network exit.
// It does not upload material, create a draft or claim write permissions.
func (m *Manager) TestConnection(ctx context.Context, app, secret string) *APIError {
	app = strings.ToLower(app)
	if !appPattern.MatchString(app) || secret == "" || len(secret) > 256 || !utf8.ValidString(secret) {
		return fail(422, "VALIDATION_FAILED", "公众号凭据格式无效")
	}
	if !m.Ready() {
		return m.stoppedError()
	}
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	_, err := m.wechat.Token(ctx, app, secret)
	if err != nil {
		return &APIError{Status: 502, Fault: err.Fault}
	}
	return nil
}
