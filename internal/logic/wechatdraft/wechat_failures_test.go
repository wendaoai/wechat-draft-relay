package wechatdraft

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

type failureTransport func(*http.Request) (*http.Response, error)

func (f failureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWechatTransportFailuresAreClassifiedWithoutCredentials(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"timeout", context.DeadlineExceeded, "timeout"},
		{"dns", &net.DNSError{Err: "private-token", Name: "private-secret"}, "dns"},
		{"tls", x509.UnknownAuthorityError{}, "tls"},
		{"refused", syscall.ECONNREFUSED, "connection_refused"},
		{"broken-pipe", &net.OpError{Op: "write", Err: syscall.EPIPE}, "connection_closed"},
		{"reset", syscall.ECONNRESET, "connection_closed"},
		{"other", errors.New("https://api.weixin.qq.com?access_token=private-token private-secret"), "connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: failureTransport(func(*http.Request) (*http.Response, error) { return nil, tc.err })}
			w := newWechat("https://api.weixin.qq.com", client)
			_, failure := w.call(context.Background(), "/cgi-bin/material/add_material", nil, strings.NewReader("private-secret"), "application/json", time.Second, false)
			if failure == nil {
				t.Fatal("failure became success")
			}
			details := failure.Fault.Details.(wechatFailureDetails)
			if details.Stage != "uploading_cover" || details.Reason != tc.reason {
				t.Fatalf("wrong classification: %#v", details)
			}
			encoded, _ := json.Marshal(failure.Fault)
			if strings.Contains(string(encoded)+logs.String(), "private-token") || strings.Contains(string(encoded)+logs.String(), "private-secret") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestWechatHTTPAndResponseFailuresPreserveSafeFacts(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body, reason string
		code         int
		faultCode    string
	}{
		{"http", 502, "private-secret private-token", "http_status", 0, "WECHAT_UNAVAILABLE"},
		{"json", 200, "private-secret private-token", "invalid_json", 0, "WECHAT_UNAVAILABLE"},
		{"errcode", 200, `{"errcode":"private-secret"}`, "invalid_errcode", 0, "WECHAT_UNAVAILABLE"},
		{"permission", 200, `{"errcode":48004,"errmsg":"private-secret"}`, "", 48004, "WECHAT_PERMISSION_DENIED"},
		{"file-type", 200, `{"errcode":40005,"errmsg":"private-secret"}`, "", 40005, "WECHAT_CONTENT_REJECTED"},
		{"quota", 200, `{"errcode":45009,"errmsg":"private-token"}`, "", 45009, "WECHAT_QUOTA_EXCEEDED"},
		{"unmapped", 200, `{"errcode":49999,"errmsg":"private-secret"}`, "", 49999, "WECHAT_REJECTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWechat("https://api.weixin.qq.com", &http.Client{Transport: failureTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})})
			_, failure := w.call(context.Background(), "/cgi-bin/material/add_material", nil, nil, "application/json", time.Second, false)
			if failure == nil || failure.Fault.Code != tc.faultCode || failure.Fault.WechatErrcode != tc.code {
				t.Fatalf("wrong failure: %#v", failure)
			}
			details := failure.Fault.Details.(wechatFailureDetails)
			if details.Stage != "uploading_cover" || details.Reason != tc.reason {
				t.Fatalf("wrong stage/reason: %#v", details)
			}
			if tc.reason == "http_status" && details.HTTPStatus != tc.status {
				t.Fatal("HTTP status lost")
			}
			encoded, _ := json.Marshal(failure.Fault)
			if strings.Contains(string(encoded), "private-secret") || strings.Contains(string(encoded), "private-token") {
				t.Fatal("upstream response leaked")
			}
		})
	}
}

func TestDraftTransportFailureRemainsUncertain(t *testing.T) {
	w := newWechat("https://api.weixin.qq.com", &http.Client{Transport: failureTransport(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })})
	_, failure := w.call(context.Background(), "/cgi-bin/draft/add", nil, nil, "application/json", time.Second, true)
	if failure == nil || !failure.Uncertain || failure.Fault.Details.(wechatFailureDetails).Stage != "submitting_draft" {
		t.Fatal("draft write uncertainty was lost")
	}
}
