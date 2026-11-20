package wechatdraft_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

// A valid ancillary PNG chunk increases transfer size without increasing
// decoded pixels. It is inserted before IEND and has a correct CRC.
func paddedPNG(size int) []byte {
	small := tinyImage()
	padding := size - len(small) - 12
	if padding < 0 {
		return small
	}
	chunk := make([]byte, padding+12)
	binary.BigEndian.PutUint32(chunk[:4], uint32(padding))
	copy(chunk[4:8], "npAD")
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	result := append([]byte(nil), small[:len(small)-12]...)
	result = append(result, chunk...)
	result = append(result, small[len(small)-12:]...)
	return result
}

func TestHTTPImagesAndMetadataReachWechatWithoutPresetLimits(t *testing.T) {
	w := newWX(t)
	c := config(t)
	h := startHarness(t, w, c)
	h.client.Timeout = 30 * time.Second
	p, token, files := payload("wx0123456789abcdef", false)
	cover := paddedPNG(11 * 1024 * 1024)
	body := paddedPNG(1000000)
	files["file_cover"] = cover
	p.Images[0].SHA256 = hash(cover)
	p.Article.Title = strings.Repeat("标题", 32)
	p.Article.Author = strings.Repeat("作者", 16)
	// Also exercise metadata persistence beyond the old 64-KiB JSON reader.
	p.Article.Digest = strings.Repeat("摘要", 12000)
	var html strings.Builder
	html.WriteString("<section><p>完整稿件验收</p>")
	for i := 0; i < 51; i++ {
		id := fmt.Sprintf("body-%02d", i)
		field := "file_" + id
		p.Images = append(p.Images, v1.Image{ID: id, Role: "body", FileField: field, SHA256: hash(body)})
		files[field] = body
		fmt.Fprintf(&html, `<img src="cw-image://%s">`, id)
	}
	html.WriteString("</section>")
	p.Article.ContentHTML = html.String()
	raw, contentType := multipartBody(p, files)
	if len(raw) <= 52<<20 {
		t.Fatal("regression fixture must exceed the former transport cap")
	}
	// Unknown Content-Length exercises the desktop client's streaming path.
	status, out, _ := h.request("POST", "/api/v1/wechat-drafts", token, p.AppID, p.UUID, contentType, io.LimitReader(bytes.NewReader(raw), int64(len(raw))))
	expect(t, status, out, 202, "")
	h.wait(p, token, "succeeded")
	_, covers, bodies, drafts := w.counts()
	if covers != 1 || bodies != 51 || drafts != 1 {
		t.Fatalf("images dropped %d %d %d", covers, bodies, drafts)
	}
	// Stored images must remain byte-identical, including bytes beyond old limits.
	for _, im := range p.Images {
		data, err := os.ReadFile(filepath.Join(taskDir(h, p), "images", im.ID+".png"))
		if err != nil || !bytes.Equal(data, files[im.FileField]) {
			t.Fatal("material was truncated or changed", im.ID, err)
		}
	}
	h.close()
	restored := startHarness(t, w, c)
	restored.wait(p, token, "succeeded")
	_, covers, bodies, drafts = w.counts()
	if covers != 1 || bodies != 51 || drafts != 1 {
		t.Fatal("restart repeated a completed WeChat write")
	}
	t.Log("62,534,336 image bytes, 51 body images, 11 MiB cover, 1 MB body images and long metadata accepted; restart preserved result")
}

func TestBoundedLocalSustainedAndJitteredRetry(t *testing.T) {
	if os.Getenv("DRAFT_LOAD_TEST") != "1" {
		t.Skip("explicit bounded local load only")
	}
	w := newWX(t)
	w.delay = 5 * time.Millisecond
	c := config(t)
	c.IPWriteLimit = 5000
	c.Workers = 2
	c.QueueSize = 4
	h := startHarness(t, w, c)
	// Eight clients, eight tasks each, a 64-KiB PNG, finite jittered retries.
	images := paddedPNG(64 << 10)
	completed := 0
	rejected := 0
	start := time.Now()
	for batch := 0; batch < 8; batch++ {
		type item struct {
			p     v1.Payload
			token string
			files map[string][]byte
		}
		pending := make([]item, 8)
		for i := range pending {
			p, token, files := payload(fmt.Sprintf("wx%016x", batch*8+i+1), false)
			files["file_cover"] = images
			p.Images[0].SHA256 = hash(images)
			pending[i] = item{p, token, files}
		}
		for round := 0; len(pending) > 0 && round < 5; round++ {
			type outcome struct {
				task   item
				status int
			}
			results := make(chan outcome, len(pending))
			for _, it := range pending {
				go func(it item) {
					s, out := h.post(it.p, it.token, it.files, false)
					if s != 429 {
						expect(t, s, out, 202, "")
						h.wait(it.p, it.token, "succeeded")
					}
					results <- outcome{it, s}
				}(it)
			}
			var next []item
			for range pending {
				result := <-results
				if result.status == 429 {
					rejected++
					next = append(next, result.task)
				} else {
					completed++
				}
			}
			pending = next
			if len(pending) > 0 {
				time.Sleep(time.Duration(50+round*19) * time.Millisecond)
			}
		}
		if len(pending) != 0 {
			t.Fatal("finite jitter retry budget exhausted")
		}
	}
	_, _, _, drafts := w.counts()
	if completed != 64 || drafts != 64 {
		t.Fatal("sustained results mismatch")
	}
	t.Logf("sustained local: tasks=%d, refused attempts=%d, cover=65536 bytes, elapsed=%s, completed/s=%.3f", completed, rejected, time.Since(start), float64(completed)/time.Since(start).Seconds())
}
