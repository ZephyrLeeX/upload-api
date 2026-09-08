//go:build !linux

package storage

import "errors"

func AvailableBytes(string) (uint64, error) {
	return 0, errors.New("disk space checks are supported only on Linux")
}
