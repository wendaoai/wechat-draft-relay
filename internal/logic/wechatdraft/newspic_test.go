package wechatdraft

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func galleryPayload() v1.Payload {
	return v1.Payload{SchemaVersion: 2, AppID: "wx0123456789abcdef", UUID: uuid.Must(uuid.NewV7()).String(), AppSecret: "fixture-secret", CoverImageID: "cover", Article: v1.Article{ArticleType: "newspic", Title: "多图消息", Digest: "图片说明", ContentHTML: `<section><img src="cw-image://body-b"><img src="cw-image://body-a"><img src="cw-image://body-b"></section>`, ImageIDs: []string{"body-b", "body-a", "body-b"}}, Images: []v1.Image{{ID: "body-a", Role: "body", FileField: "file_body-a", SHA256: strings.Repeat("a", 64)}, {ID: "body-b", Role: "body", FileField: "file_body-b", SHA256: strings.Repeat("b", 64)}, {ID: "cover", Role: "cover", FileField: "file_cover", SHA256: strings.Repeat("c", 64)}}}
}
func TestNewspicSchemaAndOrderedReferences(t *testing.T) {
	p := galleryPayload()
	if e := ValidatePayload(&p, false); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"old-schema", "no-images", "too-many", "unknown-image", "news-with-images"} {
		t.Run(bad, func(t *testing.T) {
			p := galleryPayload()
			switch bad {
			case "old-schema":
				p.SchemaVersion = 1
			case "no-images":
				p.Article.ImageIDs = nil
			case "too-many":
				p.Article.ImageIDs = make([]string, 21)
			case "unknown-image":
				p.Article.ImageIDs = []string{"unknown"}
			case "news-with-images":
				p.Article.ArticleType = "news"
			}
			if ValidatePayload(&p, false) == nil {
				t.Fatal("accepted invalid gallery")
			}
		})
	}
}
func TestNewspicWorkerUploadsPermanentMaterialsAndPreservesGalleryOnTokenRefresh(t *testing.T) {
	var uploads, writes atomic.Int32
	var sent []string
	wx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			io.WriteString(w, `{"access_token":"fixture-token","expires_in":7200}`)
		case "/cgi-bin/material/add_material":
			n := uploads.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"media_id": []string{"cover-permanent", "a-permanent", "b-permanent"}[n-1]})
		case "/cgi-bin/draft/add":
			var body struct {
				Articles []struct {
					ArticleType string `json:"article_type"`
					Content     string `json:"content"`
					Thumb       string `json:"thumb_media_id"`
					Info        struct {
						List []struct {
							ID string `json:"image_media_id"`
						} `json:"image_list"`
					} `json:"image_info"`
				} `json:"articles"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			a := body.Articles[0]
			sent = nil
			for _, im := range a.Info.List {
				sent = append(sent, im.ID)
			}
			if a.ArticleType != "newspic" || a.Content != "图片说明" || a.Thumb != "b-permanent" || !reflect.DeepEqual(sent, []string{"b-permanent", "a-permanent", "b-permanent"}) {
				t.Errorf("wrong gallery %#v", a)
			}
			if writes.Add(1) == 1 {
				io.WriteString(w, `{"errcode":40001}`)
			} else {
				io.WriteString(w, `{"media_id":"newspic-draft"}`)
			}
		default:
			t.Errorf("wrong endpoint %s", r.URL.Path)
		}
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
	var data bytes.Buffer
	png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	p := galleryPayload()
	for i := range p.Images {
		p.Images[i].SHA256 = digest(data.Bytes())
	}
	tokenHash := strings.Repeat("d", 64)
	u, _, e := m.Begin(p, tokenHash, false)
	if e != nil {
		t.Fatal(e)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, im := range p.Images {
		part, _ := w.CreateFormFile(im.FileField, "image.png")
		part.Write(data.Bytes())
	}
	w.Close()
	if _, e = u.Receive(context.Background(), multipart.NewReader(&body, w.Boundary())); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		st, e := m.Get(p.AppID, p.UUID, tokenHash)
		if e != nil {
			t.Fatal(e)
		}
		if st.Status == "succeeded" {
			break
		}
		if st.Status == "failed" || st.Status == "uncertain" || time.Now().After(deadline) {
			t.Fatalf("gallery worker: %#v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if uploads.Load() != 3 || writes.Load() != 2 {
		t.Fatal("wrong requests", uploads.Load(), writes.Load())
	}
	// Replaying the same task returns its receipt without another draft/add.
	_, receipt, e := m.Begin(p, tokenHash, false)
	if e != nil || receipt == nil || writes.Load() != 2 {
		t.Fatal("replay duplicated gallery", receipt, e)
	}
}
