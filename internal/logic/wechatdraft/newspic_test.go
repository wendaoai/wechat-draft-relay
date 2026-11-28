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
	longPost := strings.Repeat("这是一段贴图配文，用于验证多段文字原样发送。\n", 10)
	return v1.Payload{
		SchemaVersion: 2,
		AppID:         "wx0123456789abcdef",
		UUID:          uuid.Must(uuid.NewV7()).String(),
		AppSecret:     "fixture-secret",
		Article: v1.Article{
			ArticleType:     "newspic",
			Title:           "多图消息",
			Digest:          "可选短摘要",
			ContentText:     longPost,
			NeedOpenComment: 1,
			ImageIDs:        []string{"body-b", "body-a", "body-b"},
			CoverInfo: &v1.CoverInfo{
				CropPercentList: []v1.CropPercent{
					{Ratio: "1_1", X1: "0", Y1: "0.125", X2: "1", Y2: "0.875"},
					{Ratio: "16_9", X1: "0", Y1: "0.289063", X2: "1", Y2: "0.710938"},
				},
			},
		},
		Images: []v1.Image{
			{ID: "body-a", Role: "body", FileField: "file_body-a", SHA256: strings.Repeat("a", 64)},
			{ID: "body-b", Role: "body", FileField: "file_body-b", SHA256: strings.Repeat("b", 64)},
		},
	}
}

func TestNewspicSchemaAndOrderedReferences(t *testing.T) {
	p := galleryPayload()
	if e := ValidatePayload(&p, false); e != nil {
		t.Fatalf("expected valid newspic payload, got %+v", e)
	}
	for _, bad := range []string{
		"old-schema",
		"has-cover-id",
		"has-cover-role",
		"no-images",
		"too-many",
		"unknown-image",
		"orphan-body-image",
		"non-empty-html",
		"empty-content-text",
		"null-byte-content-text",
		"digest-too-long",
		"title-too-long",
		"news-with-images",
		"news-with-content-text",
		"news-with-16-9-crop",
		"crop-duplicate-ratio",
		"crop-7-decimals",
		"crop-inverted-bounds",
		"crop-out-of-range",
		"crop-schema-v1",
	} {
		t.Run(bad, func(t *testing.T) {
			p := galleryPayload()
			switch bad {
			case "old-schema":
				p.SchemaVersion = 1
			case "has-cover-id":
				p.CoverImageID = "body-a"
			case "has-cover-role":
				p.Images = append(p.Images, v1.Image{ID: "cover", Role: "cover", FileField: "file_cover", SHA256: strings.Repeat("c", 64)})
			case "no-images":
				p.Article.ImageIDs = nil
			case "too-many":
				p.Article.ImageIDs = make([]string, 21)
				for i := range p.Article.ImageIDs {
					p.Article.ImageIDs[i] = "body-a"
				}
			case "unknown-image":
				p.Article.ImageIDs = []string{"unknown"}
			case "orphan-body-image":
				p.Article.ImageIDs = []string{"body-a"}
			case "non-empty-html":
				p.Article.ContentHTML = "<section><p>不允许混传 HTML</p></section>"
			case "empty-content-text":
				p.Article.ContentText = "   \n\t  "
			case "null-byte-content-text":
				p.Article.ContentText = "正常文本\x00非法空字节"
			case "digest-too-long":
				p.Article.Digest = strings.Repeat("摘", 121)
			case "title-too-long":
				p.Article.Title = strings.Repeat("标", 33)
			case "news-with-images":
				p.Article.ArticleType = "news"
				p.Article.ContentText = ""
				p.Article.ContentHTML = `<section><img src="cw-image://body-a"></section>`
				p.CoverImageID = "cover"
				p.Images = append(p.Images, v1.Image{ID: "cover", Role: "cover", FileField: "file_cover", SHA256: strings.Repeat("c", 64)})
			case "news-with-content-text":
				p.Article.ArticleType = "news"
				p.Article.ImageIDs = nil
				p.Article.ContentHTML = `<section><p>正文</p></section>`
				p.CoverImageID = "cover"
				p.Images = []v1.Image{{ID: "cover", Role: "cover", FileField: "file_cover", SHA256: strings.Repeat("c", 64)}}
			case "news-with-16-9-crop":
				p.Article.ArticleType = "news"
				p.Article.ImageIDs = nil
				p.Article.ContentText = ""
				p.Article.ContentHTML = `<section><p>正文</p></section>`
				p.CoverImageID = "cover"
				p.Images = []v1.Image{{ID: "cover", Role: "cover", FileField: "file_cover", SHA256: strings.Repeat("c", 64)}}
			case "crop-duplicate-ratio":
				p.Article.CoverInfo.CropPercentList = []v1.CropPercent{
					{Ratio: "1_1", X1: "0", Y1: "0", X2: "1", Y2: "1"},
					{Ratio: "1_1", X1: "0.1", Y1: "0.1", X2: "0.9", Y2: "0.9"},
				}
			case "crop-7-decimals":
				p.Article.CoverInfo.CropPercentList = []v1.CropPercent{
					{Ratio: "1_1", X1: "0", Y1: "0.1234567", X2: "1", Y2: "0.876543"},
				}
			case "crop-inverted-bounds":
				p.Article.CoverInfo.CropPercentList = []v1.CropPercent{
					{Ratio: "1_1", X1: "0.8", Y1: "0.1", X2: "0.2", Y2: "0.9"},
				}
			case "crop-out-of-range":
				p.Article.CoverInfo.CropPercentList = []v1.CropPercent{
					{Ratio: "1_1", X1: "0", Y1: "0", X2: "1.000001", Y2: "1"},
				}
			case "crop-schema-v1":
				p.SchemaVersion = 1
				p.Article.ArticleType = "news"
				p.Article.ImageIDs = nil
				p.Article.ContentText = ""
				p.Article.ContentHTML = `<section><p>正文</p></section>`
				p.CoverImageID = "cover"
				p.Images = []v1.Image{{ID: "cover", Role: "cover", FileField: "file_cover", SHA256: strings.Repeat("c", 64)}}
				p.Article.CoverInfo = &v1.CoverInfo{
					CropPercentList: []v1.CropPercent{{Ratio: "1_1", X1: "0", Y1: "0", X2: "1", Y2: "1"}},
				}
			}
			if ValidatePayload(&p, false) == nil {
				t.Fatalf("accepted invalid case %s", bad)
			}
		})
	}
}

func TestNewspicWorkerUploadsGalleryWithoutSeparateCover(t *testing.T) {
	p := galleryPayload()
	t.Run("content-text", func(t *testing.T) {
		checkNewspicWorker(t, p, p.Article.ContentText, p.Article.Digest, p.Article.CoverInfo.CropPercentList)
	})
	legacy := galleryPayload()
	legacy.Article.Digest, legacy.Article.ContentText = legacy.Article.ContentText, ""
	legacy.Article.CoverInfo = nil
	legacy.Article.ContentHTML = `<section><img src="cw-image://body-b"><img src="cw-image://body-a"></section>`
	legacy.CoverImageID = "cover"
	legacy.Images = append(legacy.Images, v1.Image{ID: "cover", Role: "cover", FileField: "file_cover", SHA256: strings.Repeat("c", 64)})
	t.Run("legacy-digest", func(t *testing.T) {
		checkNewspicWorker(t, legacy, legacy.Article.Digest, "", nil)
	})
}

func checkNewspicWorker(t *testing.T, p v1.Payload, wantContent, wantDigest string, wantCrops []v1.CropPercent) {
	t.Helper()
	if e := ValidatePayload(&p, false); e != nil {
		t.Fatal(e)
	}
	var uploads, writes atomic.Int32
	wx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			io.WriteString(w, `{"access_token":"fixture-token","expires_in":7200}`)
		case "/cgi-bin/material/add_material":
			n := uploads.Add(1)
			mediaIDs := []string{"a-permanent", "b-permanent"}
			if n > int32(len(mediaIDs)) {
				t.Error("unexpected permanent material upload")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"media_id": mediaIDs[n-1]})
		case "/cgi-bin/draft/add":
			rawBody, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			var rawEnvelope struct {
				Articles []map[string]any `json:"articles"`
			}
			if err := json.Unmarshal(rawBody, &rawEnvelope); err != nil || len(rawEnvelope.Articles) != 1 {
				t.Errorf("invalid draft/add payload: %s", string(rawBody))
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			rawArticle := rawEnvelope.Articles[0]
			if _, hasThumb := rawArticle["thumb_media_id"]; hasThumb {
				t.Errorf("newspic must not send thumb_media_id: %v", rawArticle)
			}
			if _, hasSourceURL := rawArticle["content_source_url"]; hasSourceURL {
				t.Errorf("empty contentSourceUrl must be omitted: %v", rawArticle)
			}
			if _, hasDigest := rawArticle["digest"]; wantDigest == "" && hasDigest {
				t.Errorf("unexpected draft digest: %v", rawArticle["digest"])
			}
			var body struct {
				Articles []struct {
					ArticleType string `json:"article_type"`
					Digest      string `json:"digest"`
					Content     string `json:"content"`
					Info        struct {
						List []struct {
							ID string `json:"image_media_id"`
						} `json:"image_list"`
					} `json:"image_info"`
					CoverInfo struct {
						Crops []v1.CropPercent `json:"crop_percent_list"`
					} `json:"cover_info"`
				} `json:"articles"`
			}
			if err := json.Unmarshal(rawBody, &body); err != nil {
				t.Error(err)
			}
			a := body.Articles[0]
			var sent []string
			for _, im := range a.Info.List {
				sent = append(sent, im.ID)
			}
			if a.ArticleType != "newspic" ||
				a.Content != wantContent ||
				a.Digest != wantDigest ||
				!reflect.DeepEqual(sent, []string{"b-permanent", "a-permanent", "b-permanent"}) ||
				!reflect.DeepEqual(a.CoverInfo.Crops, wantCrops) {
				t.Errorf("unexpected newspic draft body: %#v", a)
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
	closeMgr := func(mgr *Manager) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		mgr.Close(ctx)
	}
	t.Cleanup(func() { closeMgr(m) })

	var data bytes.Buffer
	png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2)))
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
			wantTotal := len(p.Images) + 3
			if st.Progress.Completed != wantTotal || st.Progress.Total != wantTotal || st.Progress.Percent != 100 {
				t.Fatalf("unexpected final progress: %+v", st.Progress)
			}
			break
		}
		if st.Status == "failed" || st.Status == "uncertain" || time.Now().After(deadline) {
			t.Fatalf("gallery worker: %#v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if uploads.Load() != 2 || writes.Load() != 2 {
		t.Fatalf("wrong requests: uploads=%d writes=%d", uploads.Load(), writes.Load())
	}
	// Replaying the same task returns its receipt without another draft/add.
	_, receipt, e := m.Begin(p, tokenHash, false)
	if e != nil || receipt == nil || writes.Load() != 2 {
		t.Fatal("replay duplicated gallery", receipt, e)
	}
	closeMgr(m)

	// Restart recovery must preserve State.SchemaVersion=1 & RequestHash invariant.
	restarted, err := NewManager(context.Background(), c, NewLocalWechat(wx.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer closeMgr(restarted)
	afterRestart, e := restarted.Get(p.AppID, p.UUID, tokenHash)
	if e != nil || afterRestart.Status != "succeeded" || afterRestart.Result == nil || afterRestart.Result.MediaID != "newspic-draft" {
		t.Fatalf("restart corrupted newspic state: task=%+v err=%+v", afterRestart, e)
	}
}
