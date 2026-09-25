// Package lock provides the single merge lock. While a harvest holds it, the
// mainline working tree belongs to the harvest and nothing else writes there.
package lock

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// ErrBusy means another harvest holds the lock.
var ErrBusy = errors.New("merge lock is held by another harvest")

// Lock is a held advisory lock.
type Lock struct {
	f *os.File
}

// Acquire takes the lock without waiting.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	return &Lock{f: f}, nil
}

// Release drops the lock.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	cerr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return cerr
}
