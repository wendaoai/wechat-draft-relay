package wechatdraft

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"syscall"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

// Only structured classifications leave this layer. Raw errors can contain
// token-bearing URLs, and upstream messages can echo credentials.
type wechatFailureDetails struct {
	Stage      string `json:"stage,omitempty"`
	Reason     string `json:"reason,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
}

func wechatStage(endpoint string) string {
	switch endpoint {
	case "/cgi-bin/stable_token":
		return "getting_token"
	case "/cgi-bin/material/add_material":
		return "uploading_cover"
	case "/cgi-bin/media/uploadimg":
		return "uploading_body_images"
	case "/cgi-bin/draft/add":
		return "submitting_draft"
	case "/publisher/stat":
		return "fetching_revenue"
	}
	return ""
}

func wxFailure(endpoint, reason string, status int, draft bool) *WechatError {
	message := "微信接口暂不可用"
	switch reason {
	case "cancelled":
		message = "微信接口请求已取消"
	case "timeout":
		message = "请求微信接口超时"
	case "dns":
		message = "服务器无法解析微信接口域名"
	case "tls":
		message = "服务器与微信接口的 TLS 连接失败"
	case "connection_refused":
		message = "微信接口连接被拒绝"
	case "connection_closed":
		message = "微信接口连接中断"
	case "connection":
		message = "服务器连接微信接口失败"
	case "http2_protocol":
		message = "微信接口的 HTTP/2 上传连接异常"
	case "http_status":
		message = "微信接口返回 HTTP " + strconv.Itoa(status)
	case "response_read":
		message = "读取微信接口响应失败"
	case "response_too_large":
		message = "微信接口响应超过大小上限"
	case "invalid_json", "invalid_errcode":
		message = "微信接口返回了无法解析的响应"
	case "missing_media_id":
		message = "微信接口未返回有效素材编号"
	case "invalid_image_url":
		message = "微信接口未返回有效正文图片地址"
	case "invalid_token":
		message = "微信接口未返回有效 Token"
	case "invalid_revenue":
		message = "微信收入响应的数据结构或分页校验失败"
	}
	return &WechatError{Fault: v1.Fault{Code: "WECHAT_UNAVAILABLE", Message: message,
		Details: wechatFailureDetails{Stage: wechatStage(endpoint), Reason: reason, HTTPStatus: status}},
		Uncertain: draft, Temporary: true}
}

func wxTransportFailure(endpoint string, err error, draft bool) *WechatError {
	reason := "connection"
	var dns *net.DNSError
	var network net.Error
	var certificate *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var invalidCertificate x509.CertificateInvalidError
	var tlsRecord tls.RecordHeaderError
	switch {
	case errors.Is(err, context.Canceled):
		reason = "cancelled"
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout():
		reason = "timeout"
	case errors.As(err, &dns):
		reason = "dns"
	case errors.As(err, &certificate), errors.As(err, &unknownAuthority), errors.As(err, &invalidCertificate), errors.As(err, &tlsRecord):
		reason = "tls"
	case errors.Is(err, syscall.ECONNREFUSED):
		reason = "connection_refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		reason = "connection_closed"
	case strings.Contains(err.Error(), "PROTOCOL_ERROR") && strings.Contains(err.Error(), "stream"):
		reason = "http2_protocol"
	}
	var types []string
	for cause, depth := err, 0; cause != nil && depth < 5; cause, depth = errors.Unwrap(cause), depth+1 {
		types = append(types, fmt.Sprintf("%T", cause))
	}
	var errno syscall.Errno
	errors.As(err, &errno)
	log.Printf("wechat_request_failed stage=%s reason=%s errno=%d error_types=%s", wechatStage(endpoint), reason, errno, strings.Join(types, ","))
	return wxFailure(endpoint, reason, 0, draft)
}
