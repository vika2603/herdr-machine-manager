package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lock is the single-instance guard. The lock is held for as long as the
// process runs; the kernel releases it if the daemon dies, which is what lets
// a later start replace a socket left behind by a crash.
type lock struct{ f *os.File }

func acquireLock(path string) (*lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("daemon: another instance holds %s: %w", path, err)
	}
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return &lock{f: f}, nil
}

func (l *lock) release() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}
