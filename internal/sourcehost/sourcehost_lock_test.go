//go:build unix || windows

package sourcehost

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireOptionalLockRejectsHeldLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "sourcehost.lock")
	held, err := acquireOptionalLock(lockPath)
	if err != nil {
		t.Fatalf("acquire held lock: %v", err)
	}
	defer func() {
		if err := held.Release(); err != nil {
			t.Fatalf("release held lock: %v", err)
		}
	}()

	contender, err := acquireOptionalLock(lockPath)
	if contender != nil {
		defer func() {
			if err := contender.Release(); err != nil {
				t.Fatalf("release contender lock: %v", err)
			}
		}()
	}
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("acquire contender error = %v, want ErrLockHeld", err)
	}
}
