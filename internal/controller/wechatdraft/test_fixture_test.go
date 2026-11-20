package wechatdraft_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/google/uuid"
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"

	controller "github.com/wendaoai/wechat-draft-relay/internal/controller/wechatdraft"
	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

const testKey = "local-test-service-key-never-use-in-production"
const testSecret = "distinctive-app-secret-for-leak-detection"
const testToken = "_fake_task_token_is_not_used_"

type harness struct {
	t       *testing.T
	manager *logic.Manager
	server  *ghttp.Server
	url     string
	config  logic.Config
	wx      *wxMock
	client  *http.Client
}

func startHarness(t *testing.T, w *wxMock, c logic.Config) *harness {
	t.Helper()
	m, err := logic.NewManager(context.Background(), c, logic.NewLocalWechat(w.server.URL))
	if err != nil {
		t.Fatal(err)
	}
	s := ghttp.GetServer("draft-test-" + uuid.NewString())
	if err = controller.Configure(s, c); err != nil {
		t.Fatal(err)
	}
	controller.Bind(s, m, c.ServiceKey)

	if err = s.Start(); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, manager: m, server: s, url: fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort()), config: c, wx: w, client: &http.Client{Timeout: 10 * time.Second}}
	t.Cleanup(func() { h.close() })
	return h
}
func config(t *testing.T) logic.Config {
	c := logic.DefaultConfig()
	c.Listen = "127.0.0.1:0"
	c.DataDir = t.TempDir()
	c.ServiceKey = testKey
	return c
}

func newManager(c logic.Config, w *wxMock) (*logic.Manager, error) {
	return logic.NewManager(context.Background(), c, logic.NewLocalWechat(w.server.URL))
}
func (h *harness) close() {
	if h.manager == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.manager.Close(ctx); err != nil {
		h.t.Error(err)
	}
	h.server.Shutdown()
	h.client.CloseIdleConnections()
	h.manager = nil
}
func tinyImage() []byte {
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	png.Encode(&b, im)
	return b.Bytes()
}
func payload(app string, body bool) (v1.Payload, string, map[string][]byte) {
	id := uuid.Must(uuid.NewV7()).String()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	data := tinyImage()
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	p := v1.Payload{SchemaVersion: 1, AppID: app, UUID: id, AppSecret: testSecret, Article: v1.Article{Title: "验收文章", ContentHTML: `<section style="padding: 16px; color: #333"><p>完整正文</p></section>`}, CoverImageID: "cover", Images: []v1.Image{{ID: "cover", Role: "cover", FileField: "file_cover", SHA256: hash}}}
	files := map[string][]byte{"file_cover": data}
	if body {
		p.Article.ContentHTML = `<section><p>完整正文</p><img src="cw-image://body" alt="配图"></section>`
		p.Images = append(p.Images, v1.Image{ID: "body", Role: "body", FileField: "file_body", SHA256: hash})
		files["file_body"] = data
	}
	return p, token, files
}
func multipartBody(p v1.Payload, files map[string][]byte) ([]byte, string) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, _ := w.CreateFormField("payload")
	json.NewEncoder(part).Encode(p)
	for _, im := range p.Images {
		if data, ok := files[im.FileField]; ok {
			f, _ := w.CreateFormFile(im.FileField, "image.png")
			f.Write(data)
		}
	}
	w.Close()
	return b.Bytes(), w.FormDataContentType()
}
func (h *harness) request(method, path, token, app, id, contentType string, body io.Reader) (int, v1.Envelope, http.Header) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.url+path, body)
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("X-Draft-Token", token)
	req.Header.Set("X-Wechat-AppId", app)
	req.Header.Set("X-Draft-UUID", id)
	req.Header.Set("Content-Type", contentType)
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	var out v1.Envelope
	if err = json.NewDecoder(res.Body).Decode(&out); err != nil {
		h.t.Fatal(err)
	}
	return res.StatusCode, out, res.Header
}
func (h *harness) post(p v1.Payload, token string, files map[string][]byte, retry bool) (int, v1.Envelope) {
	path := "/api/v1/wechat-drafts"
	if retry {
		path += "/" + p.AppID + "/" + p.UUID + "/retry"
	}
	b, ct := multipartBody(p, files)
	status, out, _ := h.request("POST", path, token, p.AppID, p.UUID, ct, bytes.NewReader(b))
	return status, out
}
func task(out v1.Envelope) v1.Task {
	b, _ := json.Marshal(out.Data)
	var t v1.Task
	json.Unmarshal(b, &t)
	return t
}
func (h *harness) get(p v1.Payload, token string) (int, v1.Envelope) {
	status, out, _ := h.request("GET", "/api/v1/wechat-drafts/"+p.AppID+"/"+p.UUID, token, "", "", "", nil)
	return status, out
}
func (h *harness) wait(p v1.Payload, token string, status string) v1.Task {
	h.t.Helper()
	// Instrumented builds and WSL mounted-volume I/O need more time for the
	// maximum snapshot. Back off below the real per-task polling quota.
	deadline := time.Now().Add(30 * time.Second)
	pause := 30 * time.Millisecond
	var last v1.Task
	for time.Now().Before(deadline) {
		s, out := h.get(p, token)
		got := task(out)
		last = got
		if s == 200 && got.Status == status {
			return got
		}
		if s != 200 && s != 429 {
			h.t.Fatalf("query %d %+v", s, out)
		}
		time.Sleep(pause)
		pause = min(time.Second, pause*2)
	}
	h.t.Fatalf("task did not reach %s: last status=%s phase=%s error=%+v", status, last.Status, last.Phase, last.Error)
	return v1.Task{}
}
func expect(t *testing.T, status int, out v1.Envelope, want int, code string) {
	t.Helper()
	if status != want || (code != "" && (out.Error == nil || out.Error.Code != code)) {
		b, _ := json.Marshal(out)
		t.Fatalf("got %d %s, want %d %s", status, b, want, code)
	}
}
func taskDir(h *harness, p v1.Payload) string {
	return filepath.Join(h.config.DataDir, p.AppID, p.UUID)
}

func hash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func multipartWriter(buf *bytes.Buffer, data []byte) string {
	w := multipart.NewWriter(buf)
	part, _ := w.CreateFormField("payload")
	part.Write(data)
	w.Close()
	return w.FormDataContentType()
}
