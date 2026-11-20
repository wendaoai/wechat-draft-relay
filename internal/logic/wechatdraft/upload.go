package wechatdraft

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func (s *Store) newStage(p v1.Payload, stageID string) (string, error) {
	dir := filepath.Join(s.config.DataDir, ".staging", stageID)
	if err := s.safe(dir); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	if err := os.Mkdir(filepath.Join(dir, "images"), 0700); err != nil {
		return dir, err
	}
	err := s.writeJSON(dir, "upload.json", struct {
		ID        string `json:"id"`
		AppID     string `json:"appid"`
		UUID      string `json:"uuid"`
		CreatedAt any    `json:"createdAt"`
	}{stageID, p.AppID, p.UUID, s.config.now()}, false)
	return dir, err
}

func (s *Store) receive(ctx context.Context, dir string, p v1.Payload, reader *multipart.Reader) ([]InputFile, *APIError) {
	declared := map[string]v1.Image{}
	for _, im := range p.Images {
		declared[im.FileField] = im
	}
	seen := map[string]bool{}
	var files []InputFile
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, fail(413, "PAYLOAD_TOO_LARGE", "请求大小超限")
			}
			return nil, fail(422, "VALIDATION_FAILED", "multipart 上传不完整")
		}
		im, ok := declared[part.FormName()]
		if !ok || seen[im.ID] {
			return nil, fail(422, "VALIDATION_FAILED", "未声明或重复图片文件")
		}
		seen[im.ID] = true
		f, err := os.CreateTemp(filepath.Join(dir, "images"), ".image-*")
		if err != nil {
			return nil, storageError()
		}
		path := f.Name()
		h := sha256.New()
		if err = s.inject("imagewrite", path); err == nil {
			// The controller bounds the entire request. Stream each file intact;
			// WeChat decides whether the actual material size is accepted.
			_, err = io.Copy(io.MultiWriter(f, h), &contextReader{ctx: ctx, reader: part})
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, fail(413, "PAYLOAD_TOO_LARGE", "请求大小超限")
			}
			e := storageError()
			e.Fault.Details = map[string]string{"stage": "image_receive"}
			return nil, e
		}
		if hex.EncodeToString(h.Sum(nil)) != im.SHA256 {
			return nil, fail(422, "IMAGE_HASH_MISMATCH", "图片摘要不匹配")
		}
		format, err := decodeImage(path)
		if err != nil || (format == "gif" && im.Role != "body") {
			return nil, fail(422, "IMAGE_INVALID", "正文图片需为完整且尺寸合规的 PNG/JPEG/GIF，封面需为 PNG/JPEG")
		}
		ext := ".png"
		if format == "jpeg" {
			ext = ".jpg"
		} else if format == "gif" {
			ext = ".gif"
		}
		name := filepath.Join("images", im.ID+ext)
		if err := os.Rename(path, filepath.Join(dir, name)); err != nil {
			return nil, storageError()
		}
		files = append(files, InputFile{Name: filepath.ToSlash(name), SHA256: im.SHA256})
		if err := part.Close(); err != nil {
			return nil, fail(422, "VALIDATION_FAILED", "图片上传不完整")
		}
	}
	if len(seen) != len(declared) {
		return nil, fail(422, "IMAGE_REFERENCE_INVALID", "声明的图片文件缺失")
	}
	return files, nil
}

func decodeImage(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	c, format, err := image.DecodeConfig(f)
	if err != nil || (format != "jpeg" && format != "png" && format != "gif") || c.Width < 1 || c.Height < 1 || int64(c.Width)*int64(c.Height) > MaxImagePixels {
		return "", errors.New("invalid image config")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return "", err
	}
	if format == "gif" {
		return format, validateGIF(f)
	}
	_, decoded, err := image.Decode(f)
	if err != nil || decoded != format {
		return "", errors.New("invalid image")
	}
	return format, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}

func (s *Store) snapshot(dir string, p v1.Payload, st *State, files []InputFile) error {
	secretFree := p
	secretFree.AppSecret, secretFree.RetryRequestID, secretFree.ExpectedAttempt = "", "", 0
	secretFree.Article.ContentHTML = ""
	if err := s.writeJSON(dir, "article.json", secretFree, false); err != nil {
		return err
	}
	if err := s.writeFile(dir, "original.html", []byte(p.Article.ContentHTML), false); err != nil {
		return err
	}
	for _, name := range []string{"article.json", "original.html"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		files = append(files, InputFile{Name: name, SHA256: digest(b)})
	}
	r := Receipt{SchemaVersion: 1, AppID: p.AppID, UUID: p.UUID, TaskTokenHash: st.TaskTokenHash, RequestHash: st.RequestHash, AcceptedAt: st.AcceptedAt, Files: files}
	if err := s.writeJSON(dir, "receipt.json", r, false); err != nil {
		return err
	}
	if err := s.writeJSON(dir, "status.json", st, false); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, "upload.json")); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(dir, "images")); err != nil {
		return err
	}
	return syncDirectory(dir)
}
func stageID() string { return uuid.NewString() }
