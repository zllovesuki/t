package main

import (
	"net"

	"github.com/zllovesuki/t/multiplexer"

	"github.com/pkg/errors"
)

type multiplexerAccepter struct {
	Peer multiplexer.Peer
}

var _ net.Listener = &multiplexerAccepter{}

func (m *multiplexerAccepter) Accept() (net.Conn, error) {
	inc, ok := <-m.Peer.Handle()
	if !ok {
		return nil, errors.New("connection closed")
	}
	return inc.Conn, nil
}

func (m *multiplexerAccepter) Addr() net.Addr {
	return nil
}

func (m *multiplexerAccepter) Close() error {
	return m.Peer.Bye()
}
