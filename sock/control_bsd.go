//go:build freebsd || darwin
// +build freebsd darwin

package sock

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func Control(network, address string, conn syscall.RawConn) error {
	return conn.Control(func(fd uintptr) {
		syscall.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		syscall.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_RECVDSTADDR, 1)
	})
}
