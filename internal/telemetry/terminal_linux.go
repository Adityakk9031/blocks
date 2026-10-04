package telemetry

import (
	"syscall"
	"unsafe"
)

func isTerminal(fd uintptr) bool {
	var term syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&term)))
	return err == 0
}
