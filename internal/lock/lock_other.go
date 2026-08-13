//go:build !linux

package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func acquire(path, operation string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("another vpnctl mutation is active (%s)", operation)
	}
	if err != nil {
		return nil, err
	}
	return &Lock{release: func() error {
		closeErr := file.Close()
		removeErr := os.Remove(path)
		return errors.Join(closeErr, removeErr)
	}}, nil
}
