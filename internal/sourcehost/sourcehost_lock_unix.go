//go:build unix

package sourcehost

import (
	"errors"
	"os"
	"syscall"
)

func lockSourcehostFile(file *os.File) error {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return ErrLockHeld
		}
		return err
	}
	return nil
}

func unlockSourcehostFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
