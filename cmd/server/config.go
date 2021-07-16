package main

import (
	"github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
	"github.com/pkg/errors"
	"github.com/zllovesuki/t/server"
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
	Client struct {
		Cert string
		Key  string
	}
}

type ConfigBundle struct {
	TLS         *TLSConfig
	Web         *WebConfig
	Multiplexer *server.MultiplexerConfig
	Gossip      *server.GossipConfig
}

func getConfig(path string) (*ConfigBundle, error) {
	cfg := config.New("t")
	cfg.AddDriver(yaml.Driver)

	err := cfg.LoadExists(path)
	if err != nil {
		return nil, errors.Wrap(err, "loading config file")
	}

	var bundle ConfigBundle
	cfg.MapStruct("web", &bundle.Web)
	cfg.MapStruct("multiplexer", &bundle.Multiplexer)
	cfg.MapStruct("tls", &bundle.TLS)
	cfg.MapStruct("gossip", &bundle.Gossip)

	return &bundle, nil
}
