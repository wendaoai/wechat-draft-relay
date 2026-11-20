//go:build !windows

package wechatdraft

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockInstance(f *os.File) error       { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func atomicReplace(src, dst string) error { return os.Rename(src, dst) }
func commitRename(src, dst string) error  { return os.Rename(src, dst) }
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func diskFree(path string) (int64, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	return int64(stat.Bavail) * int64(stat.Bsize), err
}
func restrictRoot(path string) error       { return os.Chmod(path, 0700) }
func restrictConfigFile(path string) error { return os.Chmod(path, 0600) }

func sameVolume(a, b string) (bool, error) {
	var av, bv unix.Stat_t
	if err := unix.Stat(a, &av); err != nil {
		return false, err
	}
	if err := unix.Stat(b, &bv); err != nil {
		return false, err
	}
	return av.Dev == bv.Dev, nil
}
