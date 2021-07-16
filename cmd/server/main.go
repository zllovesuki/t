package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	"github.com/zllovesuki/t/server"
	"go.uber.org/zap"
)

const (
	defaultPeerPort   = 1111
	defaultGossipPort = 12345
	defaultClientPort = 9000
)

var (
	configPath = flag.String("config", "config.yaml", "path to the config.yaml")
	peerPort   = flag.Int("peerPort", defaultPeerPort, "override config peerPort")
	gossipPort = flag.Int("gossipPort", defaultGossipPort, "override config gossipPort")
	clientPort = flag.Int("clientPort", defaultClientPort, "override config clientPort")
	webPort    = flag.Int("webPort", 11101, "gateway port for forwarding to clients")
)

func main() {
	flag.Parse()

	bundle, err := getConfig(*configPath)
	if err != nil {
		panic(err)
	}
	if *peerPort != defaultPeerPort {
		bundle.Multiplexer.Peer = *peerPort
	}
	if *gossipPort != defaultGossipPort {
		bundle.Gossip.Port = *gossipPort
	}
	if *clientPort != defaultClientPort {
		bundle.Multiplexer.Client = *clientPort
	}

	peerCert, err := tls.LoadX509KeyPair(bundle.TLS.Peer.Cert, bundle.TLS.Peer.Key)
	if err != nil {
		panic(err)
	}
	clientCert, err := tls.LoadX509KeyPair(bundle.TLS.Client.Cert, bundle.TLS.Client.Key)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	caCert, err := ioutil.ReadFile(bundle.TLS.Peer.CA)
	if err != nil {
		panic(err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	peerTLSConfig := &tls.Config{
		RootCAs:                  caCertPool,
		Certificates:             []tls.Certificate{peerCert},
		ClientAuth:               tls.RequireAndVerifyClientCert,
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true,
	}

	clientTLSConfig := &tls.Config{
		Certificates:             []tls.Certificate{clientCert},
		ClientAuth:               tls.NoClientCert,
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true,
	}

	peerAddr := fmt.Sprintf("%s:%d", bundle.Multiplexer.Addr, bundle.Multiplexer.Peer)
	clientAddr := fmt.Sprintf("%s:%d", bundle.Multiplexer.Addr, bundle.Multiplexer.Client)

	peerListener, err := tls.Listen("tcp", peerAddr, peerTLSConfig)
	if err != nil {
		fmt.Printf("error listening for peers: %+v\n", err)
		return
	}
	defer peerListener.Close()

	clientListener, err := tls.Listen("tcp", clientAddr, clientTLSConfig)
	if err != nil {
		fmt.Printf("error listening for client: %+v\n", err)
		return
	}
	defer clientListener.Close()

	s, err := server.New(server.Config{
		Context:         ctx,
		Logger:          logger,
		PeerListener:    peerListener,
		PeerTLSConfig:   peerTLSConfig,
		ClientListener:  clientListener,
		ClientTLSConfig: clientTLSConfig,
		Multiplexer:     *bundle.Multiplexer,
		Gossip:          *bundle.Gossip,
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("%+v\n", string(s.Meta()))

	if err := s.Gossip(); err != nil {
		panic(err)
	}
	s.ListenForPeers()
	s.ListenForClients()
	go Gateway(ctx, logger, s, bundle)

	<-sigs
}
