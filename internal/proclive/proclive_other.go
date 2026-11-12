//go:build !linux && !darwin

package proclive

func readProcess(pid int) (Ancestor, error) {
	return Ancestor{}, ErrAncestryUnavailable
}
