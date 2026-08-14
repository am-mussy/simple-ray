package lock

import "errors"

// ErrBusy reports that another vpnctl operation holds the lock. Every other
// failure means the lock itself could not be used, which needs a different
// answer from the user than "wait and retry".
var ErrBusy = errors.New("another vpnctl mutation is active")

type Lock struct {
	release func() error
}

func Acquire(path, operation string) (*Lock, error) {
	if operation == "" {
		return nil, errors.New("operation is required")
	}
	return acquire(path, operation)
}

func (l *Lock) Release() error {
	if l == nil || l.release == nil {
		return errors.New("lock is not held")
	}
	release := l.release
	l.release = nil
	return release()
}
