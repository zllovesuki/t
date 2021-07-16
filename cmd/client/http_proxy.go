package main

import (
	"net"

	"github.com/zllovesuki/t/multiplexer"

	"github.com/pkg/errors"
)

type MultiplexerAccepter struct {
	Peer *multiplexer.Peer
}

var _ net.Listener = &MultiplexerAccepter{}

func (m *MultiplexerAccepter) Accept() (net.Conn, error) {
	inc, ok := <-m.Peer.Handle()
	if !ok {
		return nil, errors.New("connection closed")
	}
	return inc.Conn, nil
}

func (m *MultiplexerAccepter) Addr() net.Addr {
	return nil
}

func (m *MultiplexerAccepter) Close() error {
	return m.Peer.Bye()
}
