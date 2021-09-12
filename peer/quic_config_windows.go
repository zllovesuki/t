package peer

import (
	"github.com/lucas-clemente/quic-go"
)

func QUICConfig() *quic.Config {
	cfg := quicConfigCommon()
	// https://github.com/lucas-clemente/quic-go/issues/3273
	// until it is resolved, path MTU discovery is disabled on windows
	cfg.DisablePathMTUDiscovery = true
	return cfg
}
