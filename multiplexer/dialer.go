package multiplexer

import (
	"crypto/tls"
	"net"
)

// DialFunc is the function signature to help with establishing session with the peer. handshake will be used
// to negotiate on applcation layer. Connector should be passed to Config.Conn in Peer's constructor.
type DialFunc func(addr string, c *tls.Config) (connector interface{}, handshake net.Conn, closer func(), err error)

var dReg = map[Protocol]DialFunc{}

func RegisterDialer(p Protocol, d DialFunc) {
	dReg[p] = d
}

func Dialer(p Protocol) (DialFunc, error) {
	c, ok := dReg[p]
	if !ok || p == UnknownProtocol {
		return nil, ErrUnknownProtocol
	}
	return c, nil
}
