package wechatdraft

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func (s *Store) receipt(dir, app, id string) (*Receipt, error) {
	if r := s.receipts.get(dir); r != nil {
		return r, nil
	}
	var r Receipt
	if err := s.readJSON(dir, "receipt.json", &r); err != nil {
		return nil, err
	}
	if r.SchemaVersion != 1 || r.AppID != app || r.UUID != id || !hashPattern.MatchString(r.TaskTokenHash) || !hashPattern.MatchString(r.RequestHash) || r.AcceptedAt.IsZero() || len(r.Files) < 3 {
		return nil, errors.New("invalid receipt")
	}
	seen := map[string]bool{}
	for _, f := range r.Files {
		name := filepath.FromSlash(f.Name)
		valid := name == "article.json" || name == "original.html"
		if strings.HasPrefix(f.Name, "images/") && (strings.HasSuffix(f.Name, ".jpg") || strings.HasSuffix(f.Name, ".png") || strings.HasSuffix(f.Name, ".gif")) {
			base := strings.TrimSuffix(strings.TrimPrefix(f.Name, "images/"), filepath.Ext(f.Name))
			valid = imagePattern.MatchString(base)
		}
		if !valid || seen[name] || !hashPattern.MatchString(f.SHA256) {
			return nil, errors.New("invalid receipt files")
		}
		seen[name] = true
	}
	if !seen["article.json"] || !seen["original.html"] {
		return nil, errors.New("missing input manifest")
	}
	s.receipts.put(dir, &r)
	return &r, nil
}
func (s *Store) loadInput(dir string) (v1.Payload, error) {
	var p v1.Payload
	if err := s.readJSON(dir, "article.json", &p); err != nil {
		return p, err
	}
	if p.AppSecret != "" {
		return p, errors.New("persisted credentials rejected")
	}
	path := filepath.Join(dir, "original.html")
	if err := s.safe(path); err != nil {
		return p, err
	}
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxRequest+1))
	if err != nil || len(b) > int(MaxRequest) {
		return p, errors.New("invalid original HTML")
	}
	p.Article.ContentHTML = string(b)
	return p, nil
}
func (s *Store) validateInput(dir string, r *Receipt) error {
	for _, f := range r.Files {
		path := filepath.Join(dir, filepath.FromSlash(f.Name))
		if err := s.safe(path); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(file, MaxRequest+1))
		file.Close()
		if err != nil || n > MaxRequest || hex.EncodeToString(hash.Sum(nil)) != f.SHA256 {
			return errors.New("input hash mismatch")
		}
	}
	p, err := s.loadInput(dir)
	if err != nil || RequestHash(p) != r.RequestHash {
		return errors.New("input manifest mismatch")
	}
	return nil
}

func (s *Store) loadState(app, id string) (*State, *APIError) {
	dir := s.path(app, id)
	if err := s.safe(dir); err != nil {
		return nil, storageError()
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, notFound()
	} else if err != nil {
		return nil, storageError()
	}
	r, err := s.receipt(dir, app, id)
	if err != nil {
		return nil, notFound()
	}
	var rec Recovery
	if err := s.readJSON(dir, "recovery.json", &rec); !os.IsNotExist(err) {
		marker, err := s.isolate(dir)
		if err != nil {
			return nil, storageError()
		}
		rec = *marker
		if !s.config.now().Before(rec.ExpiresAt) {
			return nil, notFound()
		}
		st := initialState(v1.Payload{AppID: app, UUID: id, Images: []v1.Image{}}, r.TaskTokenHash, r.RequestHash, r.AcceptedAt)
		// Input/state may be lost, but the immutable receipt still proves the
		// accepted image count. Do not fabricate a three-step task.
		st.Progress.Total = len(r.Files) + 1
		st.Progress.Percent = 100 / st.Progress.Total
		st.Phase = "recovery_unknown"
		setFinished(st, "uncertain", &v1.Fault{Code: "STATE_CORRUPTED", Message: "任务证据不完整，请先核对公众号草稿箱"}, rec.FirstIsolatedAt)
		return st, nil
	}
	var st State
	if err := s.readJSON(dir, "status.json", &st); err != nil || !validState(&st, r) {
		if _, err := s.isolate(dir); err != nil {
			return nil, storageError()
		}
		return s.loadState(app, id)
	}
	return &st, nil
}

func (s *Store) recoverOne(app, id string) error {
	dir := s.path(app, id)
	var rec Recovery
	if err := s.readJSON(dir, "recovery.json", &rec); !os.IsNotExist(err) {
		_, err = s.isolate(dir)
		return err
	}
	r, err := s.receipt(dir, app, id)
	var st State
	if err != nil || s.readJSON(dir, "status.json", &st) != nil || !validState(&st, r) || s.validateInput(dir, r) != nil {
		_, err = s.isolate(dir)
		return err
	}
	if terminal(&st) {
		return nil
	}
	status := "failed"
	fault := &v1.Fault{Code: "SERVER_RESTARTED", Message: "服务器重启，请重新提供凭据后显式重试"}
	if st.Phase == "submitting_draft" {
		status = "uncertain"
		fault = &v1.Fault{Code: "WECHAT_RESULT_UNKNOWN", Message: "提交结果未知，请先核对公众号草稿箱"}
	}
	setFinished(&st, status, fault, s.config.now())
	return s.writeJSON(dir, "status.json", &st, true)
}
