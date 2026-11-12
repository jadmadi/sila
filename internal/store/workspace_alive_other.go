//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package store

func processAlive(pid int) bool { return true }
