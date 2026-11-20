package wechatdraft

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func TestOldQueueEntryAndCallbacksCannotTouchNewAttempt(t *testing.T) {
	var calls atomic.Int64
	var blocked atomic.Bool
	gate := make(chan struct{})
	wx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if blocked.Load() {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		io.WriteString(w, `{"errcode":40164}`)
	}))
	defer wx.Close()
	c := DefaultConfig()
	c.DataDir = t.TempDir()
	c.ServiceKey = "internal-isolated-test-key-32-bytes"
	c.Listen = "127.0.0.1:0"
	c.Workers = 1
	m, err := NewManager(context.Background(), c, NewLocalWechat(wx.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		m.Close(ctx)
	}()
	var imageBytes bytes.Buffer
	png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	data := imageBytes.Bytes()
	id := uuid.Must(uuid.NewV7()).String()
	p := v1.Payload{SchemaVersion: 1, AppID: "wx0123456789abcdef", UUID: id, AppSecret: "memory-only-test-secret", CoverImageID: "cover", Article: v1.Article{Title: "测试", ContentHTML: "<p>正文</p>"}, Images: []v1.Image{{ID: "cover", Role: "cover", FileField: "file_cover", SHA256: digest(data)}}}
	token := make([]byte, 32)
	rand.Read(token)
	tokenHash, _ := TokenHash(base64.RawURLEncoding.EncodeToString(token))
	accept := func(p v1.Payload, retry bool) {
		t.Helper()
		u, _, e := m.Begin(p, tokenHash, retry)
		if e != nil {
			t.Fatal(e)
		}
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, _ := w.CreateFormFile("file_cover", "image.png")
		part.Write(data)
		w.Close()
		reader := multipart.NewReader(&body, w.Boundary())
		if _, e = u.Receive(context.Background(), reader); e != nil {
			t.Fatal(e)
		}
	}
	accept(p, false)
	deadline := time.Now().Add(2 * time.Second)
	for {
		st, e := m.Get(p.AppID, p.UUID, tokenHash)
		if e == nil && st.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first attempt not failed")
		}
		time.Sleep(time.Millisecond)
	}
	blocked.Store(true)
	p.ExpectedAttempt = 1
	p.RetryRequestID = uuid.NewString()
	accept(p, true)
	deadline = time.Now().Add(2 * time.Second)
	for {
		if calls.Load() == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second attempt not running")
		}
		time.Sleep(time.Millisecond)
	}
	old := &job{app: p.AppID, id: p.UUID, attempt: 1, secret: "old-secret"}
	before, _ := m.Get(p.AppID, p.UUID, tokenHash)
	if m.update(old, "done", "old callback", 4, nil) {
		t.Fatal("stale progress accepted")
	}
	m.finish(old, "succeeded", nil, "must-not-be-saved")
	if _, e := m.Cancel(p.AppID, p.UUID, tokenHash, 1); e == nil || e.Fault.Code != "ATTEMPT_CONFLICT" {
		t.Fatal("old cancel changed new context")
	}
	m.mu.Lock()
	m.queue = append(m.queue, old)
	m.mu.Unlock()
	m.signal()
	after, _ := m.Get(p.AppID, p.UUID, tokenHash)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if !bytes.Equal(a, b) {
		t.Fatal("old callback changed new state")
	}
	close(gate)
	deadline = time.Now().Add(2 * time.Second)
	for {
		st, _ := m.Get(p.AppID, p.UUID, tokenHash)
		if st.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new round not finished")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatal("stale queued entry called Wechat")
	}
}

func TestReferencedTaskLocksStayExclusiveAndRelease(t *testing.T) {
	var locks taskLocks
	var active atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				unlock := locks.acquire("same-task")
				if active.Add(1) != 1 {
					t.Error("two mutexes for the same key")
				}
				active.Add(-1)
				unlock()
			}
		}()
	}
	wg.Wait()
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if len(locks.entries) != 0 {
		t.Fatal("lock registry leaked")
	}
}
