//go:build !windows

package main

import (
	"errors"
	"syscall"
)

// pidAlive reports whether a process with this pid exists. Signal 0 performs the existence
// check without delivering anything; EPERM means "exists but not ours", which still counts.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
