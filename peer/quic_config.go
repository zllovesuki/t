package peer

import (
	"github.com/lucas-clemente/quic-go"
)

func QUICConfig() *quic.Config {
	return quicConfigCommon()
}
