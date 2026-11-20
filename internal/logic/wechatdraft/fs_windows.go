//go:build windows

package wechatdraft

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockInstance(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func atomicReplace(src, dst string) error {
	a, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func commitRename(src, dst string) error {
	a, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_WRITE_THROUGH)
}

// Windows has no portable directory fsync. MoveFileEx uses WRITE_THROUGH;
// all individual files are flushed before moving the directory.
func syncDirectory(string) error { return nil }
func diskFree(path string) (int64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(p, &available, &total, &free)
	return int64(available), err
}
func restrictRoot(path string) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func restrictConfigFile(path string) error { return restrictRoot(path) }

func sameVolume(a, b string) (bool, error) {
	volume := func(path string) (string, error) {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return "", err
		}
		buf := make([]uint16, 32768)
		err = windows.GetVolumePathName(p, &buf[0], uint32(len(buf)))
		return windows.UTF16ToString(buf), err
	}
	av, err := volume(a)
	if err != nil {
		return false, err
	}
	bv, err := volume(b)
	return av == bv, err
}
