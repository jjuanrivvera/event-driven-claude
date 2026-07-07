//go:build windows

package main

// pidAlive on Windows has no cheap, reliable signal-0 equivalent; report every pid as alive
// so orphan cleanup never deletes a live session's state file. Stale files there are removed
// by clean exits only.
func pidAlive(int) bool { return true }
