//go:build windows

package wechatdraft_test

import "golang.org/x/sys/windows"

func processCPUSeconds() float64 {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0
	}
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(handle, &created, &exited, &kernel, &user) != nil {
		return 0
	}
	k := uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)
	u := uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime)
	return float64(k+u) / 1e7
}
