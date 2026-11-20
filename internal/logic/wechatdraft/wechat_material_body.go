package wechatdraft

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

type wechatMultipartBody struct {
	io.Reader
	file   *os.File
	length int64
}

func (b *wechatMultipartBody) ContentLength() int64 { return b.length }
func (b *wechatMultipartBody) Close() error         { return b.file.Close() }

// Stream the immutable image between small multipart headers with an exact
// length. This avoids chunked requests without buffering multi-megabyte images.
func materialBody(ctx context.Context, path, endpoint string, cover bool) (*wechatMultipartBody, string, *WechatError) {
	failure := func() *WechatError {
		return &WechatError{Fault: v1.Fault{Code: "LOCAL_MEDIA_UNAVAILABLE", Message: "服务器无法读取待上传图片",
			Details: wechatFailureDetails{Stage: wechatStage(endpoint), Reason: "local_file"}}}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", failure()
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
		file.Close()
		return nil, "", failure()
	}
	var envelope bytes.Buffer
	writer := multipart.NewWriter(&envelope)
	if _, err = writer.CreateFormFile("media", filepath.Base(path)); err != nil {
		file.Close()
		return nil, "", failure()
	}
	headerSize := envelope.Len()
	if err = writer.Close(); err != nil {
		file.Close()
		return nil, "", failure()
	}
	data := envelope.Bytes()
	body := &wechatMultipartBody{
		Reader: io.MultiReader(bytes.NewReader(data[:headerSize]),
			&contextReader{ctx: ctx, reader: io.LimitReader(file, info.Size())}, bytes.NewReader(data[headerSize:])),
		file: file, length: int64(len(data)) + info.Size(),
	}
	return body, writer.FormDataContentType(), nil
}
