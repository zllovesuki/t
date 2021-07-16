package server

import (
	"context"
	"crypto/tls"
	"net"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type MultiplexerConfig struct {
	Addr   string
	Peer   int
	Client int
}

type GossipConfig struct {
	Port    int
	Keyring []string
	Members []string
}

type Config struct {
	Context         context.Context
	Logger          *zap.Logger
	PeerListener    net.Listener
	PeerTLSConfig   *tls.Config
	ClientListener  net.Listener
	ClientTLSConfig *tls.Config
	Multiplexer     MultiplexerConfig
	Gossip          GossipConfig
}

func (c *Config) validate() error {
	if c.Context == nil {
		return errors.New("nil context is invalid")
	}
	if c.Logger == nil {
		return errors.New("nil logger is invalid")
	}
	if c.PeerListener == nil {
		return errors.New("nil peer listener is invalid")
	}
	if c.ClientListener == nil {
		return errors.New("nil client listener is invalid")
	}
	return nil
}
