package multiplexer

import (
	"github.com/zllovesuki/t/multiplexer/protocol"
)

type Constructor func(Config) (Peer, error)

var protocolRegistry = map[protocol.Protocol]Constructor{}

func RegisterConstructor(p protocol.Protocol, c Constructor) {
	protocolRegistry[p] = c
}

func New(p protocol.Protocol) (Constructor, error) {
	c, ok := protocolRegistry[p]
	if !ok || p == protocol.Unknown {
		return nil, protocol.ErrUnknownProtocol
	}
	return c, nil
}
