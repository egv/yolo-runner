//go:build windows

package sourcehost

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockSourcehostFile(file *os.File) error {
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&windows.Overlapped{},
	)
	if err == windows.ERROR_LOCK_VIOLATION {
		return ErrLockHeld
	}
	return err
}

func unlockSourcehostFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
