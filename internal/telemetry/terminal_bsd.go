//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package telemetry

import (
	"syscall"
	"unsafe"
)

func isTerminal(fd uintptr) bool {
	var term syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGETA, uintptr(unsafe.Pointer(&term)))
	return err == 0
}
