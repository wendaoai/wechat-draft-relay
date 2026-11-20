package wechatdraft

import (
	"io"
	"os"
	"path/filepath"
)

type directoryCursor struct {
	root, tasks *os.File
	app         string
	rootDir     string
}

func newCursor(root string) (*directoryCursor, error) {
	f, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	return &directoryCursor{root: f, rootDir: root}, nil
}
func (c *directoryCursor) close() {
	if c.tasks != nil {
		c.tasks.Close()
	}
	if c.root != nil {
		c.root.Close()
	}
}
func (c *directoryCursor) next() (string, string, error) {
	for {
		if c.tasks != nil {
			entries, err := c.tasks.ReadDir(1)
			if len(entries) > 0 {
				if !entries[0].IsDir() || entries[0].Type()&os.ModeSymlink != 0 {
					continue
				}
				return c.app, entries[0].Name(), nil
			}
			if err != nil && err != io.EOF {
				return "", "", err
			}
			c.tasks.Close()
			c.tasks = nil
		}
		entries, err := c.root.ReadDir(1)
		if len(entries) == 0 {
			return "", "", err
		}
		e := entries[0]
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || !appPattern.MatchString(e.Name()) {
			continue
		}
		c.app = e.Name()
		c.tasks, err = os.Open(filepath.Join(c.rootDir, c.app))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", "", err
		}
	}
}
