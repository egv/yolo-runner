//go:build !unix && !windows

package sourcehost

import (
	"errors"
	"os"
)

func lockSourcehostFile(_ *os.File) error {
	return errors.New("sourcehost file locking is unsupported on this platform")
}

func unlockSourcehostFile(_ *os.File) error {
	return nil
}
