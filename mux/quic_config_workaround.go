//go:build ignore
// +build ignore

package mux

import (
	"github.com/lucas-clemente/quic-go"
)

func QUICConfig() *quic.Config {
	cfg := quicConfigCommon()
	// https://github.com/lucas-clemente/quic-go/issues/3273
	// until it is resolved, path MTU discovery is disabled on windows

	// apparently, path MTU on FreeBSD is also broken
	// INTERNAL_ERROR: write udp [::]:53591->[redacted]:443: sendto: no buffer space available
	// however, this still seems to be broken.
	// TODO(zllovesuki): blocklist protocols based on GOOS

	// just disable path mtu by default for now. Too many edge cases

	cfg.DisablePathMTUDiscovery = true
	return cfg
}
