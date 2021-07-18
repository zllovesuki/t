package main

import (
	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/provider"
	"github.com/zllovesuki/t/server"

	"github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
	"github.com/pkg/errors"
)

type WebConfig struct {
	Domain string
}

type TLSConfig struct {
	Peer struct {
		CA   string
		Cert string
		Key  string
	}
}

type ConfigBundle struct {
	Network     server.Network
	TLS         TLSConfig
	Web         WebConfig
	Multiplexer server.MultiplexerConfig
	Gossip      server.GossipConfig
	ACME        acme.Config
	RFC2136     provider.RFC2136Config
}

func getConfig(path string) (*ConfigBundle, error) {
	cfg := config.New("t")
	cfg.AddDriver(yaml.Driver)

	err := cfg.LoadFiles(path)
	if err != nil {
		return nil, errors.Wrap(err, "loading config file")
	}

	var bundle ConfigBundle
	cfg.MapStruct("web", &bundle.Web)
	cfg.MapStruct("acme", &bundle.ACME)
	cfg.MapStruct("acme.provider.rfc2136", &bundle.RFC2136)
	cfg.MapStruct("network", &bundle.Network)
	cfg.MapStruct("multiplexer", &bundle.Multiplexer)
	cfg.MapStruct("tls", &bundle.TLS)
	cfg.MapStruct("gossip", &bundle.Gossip)
	bundle.ACME.Domain = "*." + bundle.Web.Domain
	bundle.ACME.RootZone = bundle.RFC2136.Zone

	return &bundle, nil
}
