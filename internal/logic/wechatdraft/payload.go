package wechatdraft

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

var cropCoordPattern = regexp.MustCompile(`^(?:0(?:\.\d{1,6})?|1(?:\.0{1,6})?)$`)

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
	isNewspic := a.ArticleType == "newspic"
	// No text field plus the original cover or HTML fields selects the legacy input.
	legacyNewspic := isNewspic && a.ContentText == "" && (p.CoverImageID != "" || a.ContentHTML != "")
	if isNewspic && p.SchemaVersion != 2 {
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
	if e := validateCoverInfo(a.CoverInfo, p.SchemaVersion, isNewspic); e != nil {
		return e
	}

	roles := map[string]string{}
	for _, im := range p.Images {
		if _, exists := roles[im.ID]; exists || !imagePattern.MatchString(im.ID) || im.FileField != "file_"+im.ID || !hashPattern.MatchString(im.SHA256) {
			return fail(422, "VALIDATION_FAILED", "图片 id、fileField 或 sha256 无效")
		}
		roles[im.ID] = im.Role
	}

	if isNewspic {
		if len(a.ImageIDs) < 1 || len(a.ImageIDs) > 20 {
			return validationFault("newspic_count", "图片消息须包含 1–20 张图片")
		}
		used := map[string]bool{}
		for _, imgID := range a.ImageIDs {
			if roles[imgID] != "body" {
				return validationFault("newspic_image", "图片消息引用了未声明的正文图片")
			}
			used[imgID] = true
		}
		if !legacyNewspic {
			return validateNewspicText(p, len(used))
		}
	} else if a.ContentText != "" {
		return validationFault("content_text_forbidden", "图文消息不能携带 contentText")
	} else if len(a.ImageIDs) != 0 {
		return validationFault("article_type", "图文消息不能携带图片消息列表")
	}
	cover := 0
	for _, im := range p.Images {
		if im.Role == "cover" && im.ID == p.CoverImageID {
			cover++
		} else if im.Role != "body" {
			return fail(422, "VALIDATION_FAILED", "图片角色或封面引用无效")
		}
	}
	if cover != 1 {
		return imageFault("cover_missing", "必须声明一张封面")
	}
	_, htmlErr := ProcessHTML(a.ContentHTML, p.Images, nil)
	return htmlErr
}

func validateNewspicText(p *v1.Payload, usedImages int) *APIError {
	a := p.Article
	if p.CoverImageID != "" {
		return imageFault("cover_forbidden", "使用 contentText 的图片消息不能声明 coverImageId")
	}
	for _, im := range p.Images {
		if im.Role != "body" {
			return imageFault("cover_forbidden", "使用 contentText 的图片消息仅允许 role=body 图片")
		}
	}
	if usedImages != len(p.Images) {
		return imageFault("image_unused", "声明的正文图片未被 imageIds 引用")
	}
	if a.ContentHTML != "" {
		return validationFault("content_html_forbidden", "使用 contentText 的图片消息不能携带 contentHtml")
	}
	if utf8.RuneCountInString(a.Title) > 32 {
		return validationFault("title_length", "图片消息标题不能超过 32 字")
	}
	if utf8.RuneCountInString(a.Digest) > newspicDigestLimit {
		return validationFault("digest_length", "摘要不能超过 120 字")
	}
	if !utf8.ValidString(a.ContentText) || strings.TrimSpace(a.ContentText) == "" || strings.ContainsRune(a.ContentText, 0) || utf8.RuneCountInString(a.ContentText) >= 20000 || len(a.ContentText) >= int(MiB) {
		return validationFault("content_text", "图片消息配文不能为空、包含空字节或超出长度限制")
	}
	return nil
}

func validateCoverInfo(info *v1.CoverInfo, schemaVersion int, isNewspic bool) *APIError {
	if info == nil {
		return nil
	}
	if schemaVersion != 2 {
		return validationFault("schema_version", "coverInfo 需要 schemaVersion 2")
	}
	if len(info.CropPercentList) == 0 {
		return validationFault("cover_info", "coverInfo.cropPercentList 不能为空")
	}
	seenRatio := map[string]bool{}
	for _, item := range info.CropPercentList {
		validRatio := item.Ratio == "1_1" || item.Ratio == "2.35_1" || (isNewspic && item.Ratio == "16_9")
		if !validRatio || seenRatio[item.Ratio] {
			return validationFault("cover_info", "coverInfo 比例不支持或存在重复")
		}
		seenRatio[item.Ratio] = true
		x1, ok1 := parseCropCoord(item.X1)
		y1, ok2 := parseCropCoord(item.Y1)
		x2, ok3 := parseCropCoord(item.X2)
		y2, ok4 := parseCropCoord(item.Y2)
		if !ok1 || !ok2 || !ok3 || !ok4 || x1 >= x2 || y1 >= y2 {
			return validationFault("cover_info", "coverInfo 坐标须为 [0, 1] 内最多 6 位小数且满足 x1 < x2、y1 < y2")
		}
	}
	return nil
}

func parseCropCoord(raw string) (float64, bool) {
	if !cropCoordPattern.MatchString(raw) {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 1 {
		return 0, false
	}
	return v, true
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
