//go:build !windows
// +build !windows

package peer

import (
	"github.com/lucas-clemente/quic-go"
)

func QUICConfig() *quic.Config {
	return quicConfigCommon()
}
