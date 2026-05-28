//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

func lockTrackerWatchFile(file *os.File) error {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errTrackerWatchLockHeld
		}
		return err
	}
	return nil
}

func unlockTrackerWatchFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
