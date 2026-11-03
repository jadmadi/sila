//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package goals

import (
	"errors"
	"os"
)

// flockSupported reports whether this platform provides advisory file locks.
const flockSupported = false

// tryLockFile is never called when flockSupported is false; it exists so the
// claim code compiles on every platform.
func tryLockFile(*os.File) error { return errFlockUnsupported }

// unlockFile is a no-op on platforms without advisory locks.
func unlockFile(*os.File) {}

// errFlockUnsupported marks platforms that fall back to the sentinel lock.
var errFlockUnsupported = errors.New("advisory file locks are not supported on this platform")
