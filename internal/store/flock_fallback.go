//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package store

import (
	"errors"
	"os"
)

var errFlockBusy = errors.New("file lock busy")

func tryFlockExclusive(f *os.File) error {
	return nil
}

func unlockFlock(f *os.File) error {
	return nil
}
