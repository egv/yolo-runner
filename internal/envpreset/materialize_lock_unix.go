//go:build unix

package envpreset

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type materializeLock struct {
	path string
	file *os.File
}

func acquireMaterializeLock(key string) (*materializeLock, error) {
	lockPath := materializeLockPath(key)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create workspace lock directory: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open workspace lock at %s: %w", lockPath, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire workspace lock at %s: %w", lockPath, err)
	}
	return &materializeLock{path: lockPath, file: file}, nil
}

func (l *materializeLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("release workspace lock at %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close workspace lock at %s: %w", l.path, closeErr)
	}
	return nil
}
