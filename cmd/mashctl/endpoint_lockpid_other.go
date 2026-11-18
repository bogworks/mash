//go:build !unix

package main

// epPidState cannot tell whether a process runs on this platform: only the age rule reclaims a lock here.
func epPidState(pid int) int { return -1 }
