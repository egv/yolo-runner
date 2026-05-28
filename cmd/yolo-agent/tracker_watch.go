package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type trackerWatchLock struct {
	path string
	file *os.File
}

func defaultRunTrackerWatch(_ context.Context, cfg trackerWatchConfig) error {
	trackerAgentConfig, err := newTrackerConfigService().ResolveTrackerAgentConfig(cfg.repoRoot)
	if err != nil {
		return err
	}
	lock, err := acquireTrackerWatchLock(trackerAgentConfig.LockPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Release()
	}()
	return nil
}

func acquireTrackerWatchLock(lockPath string) (*trackerWatchLock, error) {
	lockPath = strings.TrimSpace(lockPath)
	if lockPath == "" {
		return nil, errors.New("tracker-watch lock path is required")
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("cannot create tracker-watch lock directory for %s: %w", lockPath, err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cannot open tracker-watch lock at %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("tracker-watch lock is already held at %s", lockPath)
		}
		return nil, fmt.Errorf("cannot acquire tracker-watch lock at %s: %w", lockPath, err)
	}
	if err := file.Truncate(0); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("cannot update tracker-watch lock at %s: %w", lockPath, err)
	}
	if _, err := fmt.Fprintf(file, "pid=%d\n", os.Getpid()); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("cannot update tracker-watch lock at %s: %w", lockPath, err)
	}
	return &trackerWatchLock{
		path: lockPath,
		file: file,
	}, nil
}

func (l *trackerWatchLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("cannot release tracker-watch lock at %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("cannot close tracker-watch lock at %s: %w", l.path, closeErr)
	}
	return nil
}
