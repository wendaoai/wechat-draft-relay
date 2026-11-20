package wechatdraft

import (
	"context"
	"net/url"
	"strings"
	"time"
)

func (w *Wechat) Material(ctx context.Context, token, path string, cover bool) (string, *WechatError) {
	endpoint := "/cgi-bin/media/uploadimg"
	query := url.Values{"access_token": {token}}
	if cover {
		endpoint = "/cgi-bin/material/add_material"
		query.Set("type", "image")
	}
	var last *WechatError
	for n := 1; n <= 2; n++ {
		body, contentType, localErr := materialBody(ctx, path, endpoint, cover)
		if localErr != nil {
			return "", localErr
		}
		obj, err := w.call(ctx, endpoint, query, body, contentType, 60*time.Second, false)
		body.Close()
		if err == nil {
			field := "url"
			if cover {
				field = "media_id"
			}
			value := rawString(obj, field)
			if value != "" && (!cover || validMediaID(value)) && (cover || wechatImageURL(value)) {
				return value, nil
			}
			reason := "invalid_image_url"
			if cover {
				reason = "missing_media_id"
			}
			err = wxFailure(endpoint, reason, 200, false)
		}
		last = err
		if !err.Temporary || n == 2 {
			break
		}
		if w.backoff(ctx, n) != nil {
			break
		}
	}
	return "", last
}
func wechatImageURL(value string) bool {
	if !webURL(value) {
		return false
	}
	u, _ := url.Parse(value)
	host := strings.ToLower(u.Hostname())
	return host == "mmbiz.qpic.cn" || strings.HasSuffix(host, ".qpic.cn") || strings.HasSuffix(host, ".qlogo.cn")
}
