//go:build !plan9 && !windows && !wasm
// +build !plan9,!windows,!wasm

package sock

import "syscall"

func Control(network, address string, conn syscall.RawConn) error {
	return conn.Control(func(fd uintptr) {
		syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_PKTINFO, 1)
	})
}
