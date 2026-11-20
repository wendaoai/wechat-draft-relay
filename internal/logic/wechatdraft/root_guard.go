package wechatdraft

import (
	"errors"
	"os"
	"strings"
)

// Do not change permissions on a directory that contains unrelated user data.
// A persistent root is dedicated to this service, not a general project folder.
func validateOwnedRoot(root string) error {
	return eachEntry(root, func(entry os.DirEntry) error {
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink rejected")
		}
		name := entry.Name()
		if entry.IsDir() && (name == ".staging" || appPattern.MatchString(name)) {
			return nil
		}
		if !entry.IsDir() && (name == ".instance.lock" || strings.HasPrefix(name, ".ready-")) {
			return nil
		}
		return errors.New("data directory contains unrelated entries")
	})
}
