package sock

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func Control(network, address string, conn syscall.RawConn) error {
	return conn.Control(func(fd uintptr) {
		windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
		windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, windows.IP_PKTINFO, 1)
	})
}
