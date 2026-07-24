//go:build windows

package health

import "golang.org/x/sys/windows"

func freeDiskSpace(path string) (uint64, error) {
	var freeBytes uint64
	err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(path), &freeBytes, nil, nil)
	return freeBytes, err
}
