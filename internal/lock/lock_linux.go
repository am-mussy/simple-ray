//go:build linux

package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquire(path, operation string) (*Lock, error) {
	parent := filepath.Dir(path)
	if err := ensureRuntimeDirectory(parent); err != nil {
		return nil, err
	}

	fd, err := unix.Open(path, unix.O_CLOEXEC|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open mutation lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	closeOnError := func(err error) (*Lock, error) {
		_ = file.Close()
		return nil, err
	}

	var status unix.Stat_t
	if err := unix.Fstat(fd, &status); err != nil {
		return closeOnError(fmt.Errorf("inspect mutation lock: %w", err))
	}
	if status.Mode&unix.S_IFMT != unix.S_IFREG || status.Uid != uint32(os.Geteuid()) || status.Nlink != 1 || status.Mode&0777 != 0600 {
		return closeOnError(errors.New("mutation lock file has unsafe ownership, type, or permissions"))
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return closeOnError(fmt.Errorf("%w (%s)", ErrBusy, operation))
		}
		return closeOnError(fmt.Errorf("acquire mutation lock: %w", err))
	}

	return &Lock{release: func() error {
		unlockErr := unix.Flock(fd, unix.LOCK_UN)
		closeErr := file.Close()
		return errors.Join(unlockErr, closeErr)
	}}, nil
}

func ensureRuntimeDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create mutation lock directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect mutation lock directory: %w", err)
	}
	var status unix.Stat_t
	if err := unix.Lstat(path, &status); err != nil {
		return fmt.Errorf("inspect mutation lock directory ownership: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || status.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0700 {
		return errors.New("mutation lock directory has unsafe ownership, type, or permissions")
	}
	return nil
}
