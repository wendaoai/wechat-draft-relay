package wechatdraft

import (
	"context"
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func (w *Wechat) materialFor(ctx context.Context, app, secret, token, path string, cover bool) (string, string, *WechatError) {
	value, err := w.Material(ctx, token, path, cover)
	if err == nil || !err.TokenInvalid {
		return value, token, err
	}
	token, err = w.Refresh(ctx, app, secret, token)
	if err != nil {
		return "", token, err
	}
	value, err = w.Material(ctx, token, path, cover)
	return value, token, err
}
func (w *Wechat) draftFor(ctx context.Context, app, secret, token string, a v1.Article, content, cover string, gallery ...string) (string, *WechatError) {
	value, err := w.Draft(ctx, token, a, content, cover, gallery...)
	if err == nil || !err.TokenInvalid {
		return value, err
	}
	token, err = w.Refresh(ctx, app, secret, token)
	if err != nil {
		return "", err
	}
	return w.Draft(ctx, token, a, content, cover, gallery...)
}
