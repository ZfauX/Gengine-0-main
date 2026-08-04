//go:build linux || darwin

package health

import "syscall"

// freeDiskSpace возвращает количество свободных байт на диске для указанного пути.
func freeDiskSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
