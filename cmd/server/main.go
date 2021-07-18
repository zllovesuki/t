package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/gateway"
	"github.com/zllovesuki/t/provider"
	"github.com/zllovesuki/t/server"

	"go.uber.org/zap"
)

const (
	defaultPeerPort   = 1111
	defaultGossipPort = 12345
	defaultClientPort = 9000
)

var (
	disableAcme = flag.Bool("disableACME", false, "disable acme functions and use bundle.json as it")
	configPath  = flag.String("config", "config.yaml", "path to the config.yaml")
	peerPort    = flag.Int("peerPort", defaultPeerPort, "override config peerPort")
	gossipPort  = flag.Int("gossipPort", defaultGossipPort, "override config gossipPort")
	clientPort  = flag.Int("clientPort", defaultClientPort, "override config clientPort")
	webPort     = flag.Int("webPort", 443, "gateway port for forwarding to clients")
	debug       = flag.Bool("debug", false, "verbose logging")
)

func main() {
	flag.Parse()

	var logger *zap.Logger
	var err error

	if *debug {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}
	if err != nil {
		log.Fatal(err)
	}

	bundle, err := getConfig(*configPath)
	if err != nil {
		logger.Fatal("loading config file", zap.Error(err))
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
		logger.Fatal("loading peer certificates", zap.Error(err))
	}

	// TODO(zllovesuki): make it configurable
	rfc2136, err := provider.NewRFC2136Provider(bundle.RFC2136)
	if err != nil {
		logger.Fatal("setting up rfc2136 provider", zap.Error(err))
	}

	bundle.ACME.DNSProvider = rfc2136
	certManager, err := acme.New(bundle.ACME)
	if err != nil {
		logger.Fatal("setting up cert manager", zap.Error(err))
	}

	if *disableAcme {
		logger.Warn("ACME is disabled: no cert checking nor issuance.")
	}
	certManager.LoadAccountFromFile()
	certManager.LoadBundleFromFile()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	caCert, err := ioutil.ReadFile(bundle.TLS.Peer.CA)
	if err != nil {
		logger.Fatal("reading CA bundle", zap.Error(err))
	}
	caCertPool := x509.NewCertPool()
	if ok := caCertPool.AppendCertsFromPEM(caCert); !ok {
		logger.Fatal("unable to use the provided CA bundle")
	}

	peerTLSConfig := &tls.Config{
		Rand:                     rand.Reader,
		RootCAs:                  caCertPool,
		ClientCAs:                caCertPool,
		Certificates:             []tls.Certificate{peerCert},
		ClientAuth:               tls.RequireAndVerifyClientCert,
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true,
		VerifyConnection:         checkPeerSAN("t_Peer"),
	}

	clientTLSConfig := &tls.Config{
		Rand:                     rand.Reader,
		GetCertificate:           certManager.GetCertificatesFunc,
		ClientAuth:               tls.NoClientCert,
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true,
		VerifyConnection:         checkClientSNI(bundle.Web.Domain),
	}

	gatwayTLSConfig := &tls.Config{
		Rand:                     rand.Reader,
		GetCertificate:           certManager.GetCertificatesFunc,
		ClientAuth:               tls.NoClientCert,
		NextProtos:               []string{"http/1.1"},
		MinVersion:               tls.VersionTLS11,
		PreferServerCipherSuites: true,
		VerifyConnection:         checkClientSNI(bundle.Web.Domain),
	}

	peerAddr := fmt.Sprintf("%s:%d", bundle.Network.BindAddr, bundle.Multiplexer.Peer)
	clientAddr := fmt.Sprintf("%s:%d", bundle.Network.BindAddr, bundle.Multiplexer.Client)
	gatewayAddr := fmt.Sprintf("%s:%d", bundle.Network.BindAddr, *webPort)

	peerListener, err := tls.Listen("tcp", peerAddr, peerTLSConfig)
	if err != nil {
		logger.Fatal("listening for peer connection", zap.Error(err))
	}
	defer peerListener.Close()

	clientListener, err := tls.Listen("tcp", clientAddr, clientTLSConfig)
	if err != nil {
		logger.Fatal("listening for client connection", zap.Error(err))
	}
	defer clientListener.Close()

	gatewayListener, err := tls.Listen("tcp", gatewayAddr, gatwayTLSConfig)
	if err != nil {
		logger.Fatal("listening for gateway connection", zap.Error(err))
	}
	defer gatewayListener.Close()

	domain := bundle.Web.Domain
	if *webPort != 443 {
		domain = fmt.Sprintf("%s:%d", domain, *webPort)
	}
	s, err := server.New(server.Config{
		Context:        ctx,
		Logger:         logger,
		Network:        bundle.Network,
		PeerListener:   peerListener,
		PeerTLSConfig:  peerTLSConfig,
		ClientListener: clientListener,
		Multiplexer:    bundle.Multiplexer,
		Gossip:         bundle.Gossip,
		CertManager:    certManager,
		DisableACME:    *disableAcme,
		Domain:         domain,
	})
	if err != nil {
		logger.Fatal("setting up multiplexer server", zap.Error(err))
	}

	g, err := gateway.New(gateway.GatewayConfig{
		Logger:      logger,
		Multiplexer: s,
		Listener:    gatewayListener,
		RootDomain:  bundle.Web.Domain,
		ClientPort:  bundle.Multiplexer.Client,
	})
	if err != nil {
		logger.Fatal("setting up gateway server", zap.Error(err))
	}

	if err := s.Gossip(); err != nil {
		logger.Fatal("starting gossip", zap.Error(err))
	}
	s.ListenForPeers()
	s.ListenForClients()

	go g.Start(ctx)

	logger.Info("peer info",
		zap.String("bindAddr", peerAddr),
		zap.String("advertiseAddr", fmt.Sprintf("%s:%d", bundle.Network.AdvertiseAddr, bundle.Multiplexer.Peer)),
		zap.Uint64("PeerID", s.PeerID()),
	)

	<-sigs
}
