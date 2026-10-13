//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !windows

package telemetry

func isTerminal(uintptr) bool { return false }
