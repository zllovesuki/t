package server

import (
	"context"
	"crypto/tls"
	"net"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/multiplexer"

	"github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type MultiplexerConfig struct {
	Peer     int
	Client   int
	Protocol multiplexer.Protocol
}

type GossipConfig struct {
	Port    int
	Keyring []string
	Members []string
}

type Network struct {
	AdvertiseAddr string
	BindAddr      string
}

type Config struct {
	Context            context.Context
	Logger             *zap.Logger
	Network            Network
	PeerTLSListener    net.Listener
	PeerQUICListener   quic.Listener
	PeerTLSConfig      *tls.Config
	ClientTLSListener  net.Listener
	ClientQUICListener quic.Listener
	Multiplexer        MultiplexerConfig
	Gossip             GossipConfig
	CertManager        *acme.CertManager
	Debug              bool
	Domain             string
}

func (c *Config) validate() error {
	if c.Context == nil {
		return errors.New("nil context is invalid")
	}
	if c.Logger == nil {
		return errors.New("nil logger is invalid")
	}
	if c.PeerTLSListener == nil {
		return errors.New("nil peer tls listener is invalid")
	}
	if c.PeerQUICListener == nil {
		return errors.New("nil peer quic listener is invalid")
	}
	if c.PeerTLSConfig == nil {
		return errors.New("nil peer tls config is invalid")
	}
	if c.ClientTLSListener == nil {
		return errors.New("nil client tls listener is invalid")
	}
	if c.ClientQUICListener == nil {
		return errors.New("nil client quic listener is invalid")
	}
	if c.CertManager == nil {
		return errors.New("nil cert manager is invalid")
	}
	if c.Domain == "" {
		return errors.New("empty domain is invalid")
	}
	return nil
}
