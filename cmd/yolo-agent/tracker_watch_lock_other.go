//go:build !unix && !windows

package main

import (
	"errors"
	"os"
)

func lockTrackerWatchFile(_ *os.File) error {
	return errors.New("tracker-watch file locking is unsupported on this platform")
}

func unlockTrackerWatchFile(_ *os.File) error {
	return nil
}
