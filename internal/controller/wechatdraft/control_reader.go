package wechatdraft

import (
	"io"
	"net/http"

	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

// Bound MIME headers as well as JSON before multipart's header parser can
// allocate its own (much larger) default header allowance.
type controlReader struct {
	reader    io.Reader
	remaining int64
	admitted  bool
}

func (r *controlReader) Read(p []byte) (int, error) {
	if r.admitted {
		return r.reader.Read(p)
	}
	if r.remaining <= 0 {
		return 0, &http.MaxBytesError{Limit: logic.MaxRequest}
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}
