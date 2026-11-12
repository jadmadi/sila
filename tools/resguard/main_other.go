//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "resguard: only Linux is supported; the run would be unguarded on this platform")
	os.Exit(1)
}
