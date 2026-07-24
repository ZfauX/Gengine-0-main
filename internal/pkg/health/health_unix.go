//go:build !windows

package health

func freeDiskSpace(path string) (uint64, error) { return 0, nil }
