package main

import (
	"net"

	"github.com/pkg/errors"
)

type multiplexerAccepter struct {
	ConnCh chan net.Conn
}

var _ net.Listener = &multiplexerAccepter{}

func (m *multiplexerAccepter) Accept() (net.Conn, error) {
	c, ok := <-m.ConnCh
	if !ok {
		return nil, errors.New("connection closed")
	}
	return c, nil
}

func (m *multiplexerAccepter) Addr() net.Addr {
	return nil
}

func (m *multiplexerAccepter) Close() error {
	return nil
}
