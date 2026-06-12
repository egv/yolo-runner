//go:build !unix

package envpreset

import "fmt"

type materializeLock struct{}

func acquireMaterializeLock(string) (*materializeLock, error) {
	return nil, fmt.Errorf("workspace flock is unsupported on this platform")
}

func (*materializeLock) Release() error {
	return nil
}
