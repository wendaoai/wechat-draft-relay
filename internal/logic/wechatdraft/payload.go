package wechatdraft

import (
	"net/url"
	"strings"
	"unicode/utf8"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func ValidatePayload(p *v1.Payload, retry bool) *APIError {
	app, id, err := Identity(p.AppID, p.UUID)
	if err != nil {
		return err
	}
	p.AppID, p.UUID = app, id
	if (p.SchemaVersion != 1 && p.SchemaVersion != 2) || len(p.AppSecret) > 256 || !utf8.ValidString(p.AppSecret) {
		return fail(422, "VALIDATION_FAILED", "schemaVersion 或 appsecret 无效")
	}
	if retry && (p.ExpectedAttempt < 1 || !OperationID(p.RetryRequestID)) {
		return fail(422, "VALIDATION_FAILED", "重试需要 expectedAttempt 和 UUID v4 retryRequestId")
	}
	p.RetryRequestID = strings.ToLower(p.RetryRequestID)
	if !retry && (p.ExpectedAttempt != 0 || p.RetryRequestID != "") {
		return fail(422, "VALIDATION_FAILED", "创建不能携带重试操作字段")
	}
	a := &p.Article
	if a.ArticleType != "" && a.ArticleType != "news" && a.ArticleType != "newspic" {
		return validationFault("article_type", "文章类型无效")
	}
	if a.ArticleType == "newspic" && p.SchemaVersion != 2 {
		return validationFault("schema_version", "图片消息需要 schemaVersion 2")
	}
	if strings.TrimSpace(a.Title) == "" {
		return validationFault("title_empty", "标题不能为空")
	}
	if a.NeedOpenComment < 0 || a.NeedOpenComment > 1 || a.OnlyFansCanComment < 0 || a.OnlyFansCanComment > 1 {
		return fail(422, "VALIDATION_FAILED", "评论开关只能为 0 或 1")
	}
	if a.NeedOpenComment == 0 {
		a.OnlyFansCanComment = 0
	}
	if len(a.ContentSourceURL) > 1024 || (a.ContentSourceURL != "" && !webURL(a.ContentSourceURL)) {
		return fail(422, "VALIDATION_FAILED", "原文地址必须是 http/https")
	}
	ids := map[string]bool{}
	cover := 0
	for _, im := range p.Images {
		if !imagePattern.MatchString(im.ID) || ids[im.ID] || im.FileField != "file_"+im.ID || !hashPattern.MatchString(im.SHA256) {
			return fail(422, "VALIDATION_FAILED", "图片 id、fileField 或 sha256 无效")
		}
		ids[im.ID] = true
		if im.Role == "cover" && im.ID == p.CoverImageID {
			cover++
		} else if im.Role != "body" {
			return fail(422, "VALIDATION_FAILED", "图片角色或封面引用无效")
		}
	}
	if cover != 1 {
		return imageFault("cover_missing", "必须声明一张封面")
	}
	if a.ArticleType == "newspic" {
		if len(a.ImageIDs) < 1 || len(a.ImageIDs) > 20 {
			return validationFault("newspic_count", "图片消息须包含 1–20 张图片")
		}
		body := map[string]bool{}
		for _, im := range p.Images {
			if im.Role == "body" {
				body[im.ID] = true
			}
		}
		for _, id := range a.ImageIDs {
			if !body[id] {
				return validationFault("newspic_image", "图片消息引用了未声明的正文图片")
			}
		}
	} else if len(a.ImageIDs) != 0 {
		return validationFault("article_type", "图文消息不能携带图片消息列表")
	}
	_, htmlErr := ProcessHTML(a.ContentHTML, p.Images, nil)
	return htmlErr
}

func validationFault(reason, message string) *APIError {
	e := fail(422, "VALIDATION_FAILED", message)
	e.Fault.Details = map[string]string{"reason": reason}
	return e
}

func webURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}
