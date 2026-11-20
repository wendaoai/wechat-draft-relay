package wechatdraft

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Store struct {
	config   Config
	lockFile *os.File
	budget   DiskBudget
	receipts receiptCache
}

func openStore(c Config) (*Store, error) {
	if err := os.MkdirAll(c.DataDir, 0700); err != nil {
		return nil, err
	}
	s := &Store{config: c, budget: DiskBudget{Limit: c.DiskQuota}}
	if err := s.safe(c.DataDir); err != nil {
		return nil, err
	}
	if err := validateOwnedRoot(c.DataDir); err != nil {
		return nil, err
	}
	if err := s.safe(filepath.Join(c.DataDir, ".instance.lock")); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(c.DataDir, ".instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockInstance(f); err != nil {
		f.Close()
		return nil, errors.New("data directory is already locked")
	}
	s.lockFile = f
	if err := restrictRoot(c.DataDir); err != nil {
		f.Close()
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(c.DataDir, ".staging"), 0700); err != nil {
		f.Close()
		return nil, err
	}
	n, err := treeSize(c.DataDir)
	if err != nil {
		f.Close()
		return nil, err
	}
	s.budget.Used = n
	return s, nil
}
func (s *Store) path(app, id string) string { return filepath.Join(s.config.DataDir, app, id) }
func (s *Store) inject(op, path string) error {
	if s.config.Fault != nil {
		return s.config.Fault(op, path)
	}
	return nil
}

// The service owns this private root. Reject symlinks in the entire ancestry,
// including existing ancestors above the root, and in all task files.
func (s *Store) safe(path string) error {
	rel, err := filepath.Rel(s.config.DataDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("unsafe path")
	}
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink rejected")
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
	}
	return nil
}
func (s *Store) readJSON(dir, name string, out any) error {
	path := filepath.Join(dir, name)
	if err := s.safe(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	limit := int64(65536)
	if name == "article.json" || name == "receipt.json" {
		limit = MaxRequest
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return errors.New("invalid JSON file size")
	}
	return json.Unmarshal(b, out)
}
func (s *Store) writeJSON(dir, name string, value any, account bool) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.writeFile(dir, name, b, account)
}
func (s *Store) writeFile(dir, name string, b []byte, account bool) error {
	// The caller serializes this task; distinct tasks can flush independently.
	// Disk accounting has its own short atomic critical section.
	path := filepath.Join(dir, name)
	if err := s.safe(path); err != nil {
		return err
	}
	if err := s.inject("write", path); err != nil {
		return err
	}
	oldSize := int64(0)
	if info, err := os.Stat(path); err == nil {
		oldSize = info.Size()
	}
	f, err := os.CreateTemp(dir, ".write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		f.Close()
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) && account {
			if info, e := os.Stat(tmp); e == nil {
				s.budget.adjust(info.Size())
			}
		}
	}()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = s.inject("sync", path); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = s.inject("replace", path); err != nil {
		return err
	}
	if err = atomicReplace(tmp, path); err != nil {
		return err
	}
	if account {
		s.budget.adjust(int64(len(b)) - oldSize)
	}
	if err = s.inject("dirsync", path); err != nil {
		return err
	}
	return syncDirectory(dir)
}
func (s *Store) commit(stage, dest string) error {
	if filepath.Dir(stage) != filepath.Join(s.config.DataDir, ".staging") {
		return errors.New("commit source must be same-volume staging")
	}
	if err := s.safe(dest); err != nil {
		return err
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return errors.New("destination exists")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	if ok, err := sameVolume(stage, filepath.Dir(dest)); err != nil || !ok {
		return errors.New("cross-volume commit rejected")
	}
	if err := s.inject("commit", dest); err != nil {
		return err
	}
	if err := syncDirectory(stage); err != nil {
		return err
	}
	if err := commitRename(stage, dest); err != nil {
		return err
	}
	if err := s.inject("commitsync", dest); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(dest)); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(stage))
}
func (s *Store) remove(path string) (int64, error) {
	if path == s.config.DataDir || path == filepath.Join(s.config.DataDir, ".staging") {
		return 0, errors.New("cannot remove root")
	}
	if err := s.safe(path); err != nil {
		return 0, err
	}
	before, err := treeSize(path)
	if err != nil {
		return 0, err
	}
	if err = s.inject("remove", path); err != nil {
		return 0, err
	}
	err = os.RemoveAll(path)
	after, sizeErr := treeSize(path)
	if sizeErr != nil {
		return 0, sizeErr
	}
	return before - after, err
}

// ReadDir batches bound memory even for large account and task directories.
func eachEntry(dir string, visit func(os.DirEntry) error) error {
	f, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		entries, readErr := f.ReadDir(100)
		for _, entry := range entries {
			if err := visit(entry); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
func treeSize(path string) (int64, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("symlink rejected")
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	var total int64
	err = eachEntry(path, func(entry os.DirEntry) error {
		n, err := treeSize(filepath.Join(path, entry.Name()))
		total += n
		return err
	})
	return total, err
}
