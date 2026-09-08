//go:build !linux

package storage

import "errors"

func sameFilesystem(string, string) (bool, error) {
	return false, errors.New("filesystem identity checks are supported only on Linux")
}

func AvailableBytes(string) (uint64, error) {
	return 0, errors.New("disk space checks are supported only on Linux")
}
