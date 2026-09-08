//go:build linux

package storage

import (
	"os"
	"syscall"
)

func sameFilesystem(a, b string) (bool, error) {
	aInfo, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	aStat, ok := aInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return false, syscall.EINVAL
	}
	bStat, ok := bInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return false, syscall.EINVAL
	}
	return aStat.Dev == bStat.Dev, nil
}

func AvailableBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
