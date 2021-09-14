package mux

import (
	"net"

	"github.com/lucas-clemente/quic-go"
)

func WrapQUIC(session quic.Session, stream quic.Stream) net.Conn {
	return &quicConn{
		Stream:  stream,
		Session: session,
	}
}
