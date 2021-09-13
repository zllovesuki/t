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
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/gateway"
	"github.com/zllovesuki/t/peer"
	"github.com/zllovesuki/t/profiler"
	"github.com/zllovesuki/t/provider"
	"github.com/zllovesuki/t/server"

	"github.com/lucas-clemente/quic-go"
	"go.uber.org/zap"
)

var Version = "dev"

const (
	defaultPeerPort   = 1111
	defaultGossipPort = 12345
)

var (
	gatewayPort = flag.Int("gatewayPort", 443, "port for accepting clients and visitors")
	profile     = flag.String("profiler", "127.0.0.1:9090", "where the profiler should listen for connections")
	configPath  = flag.String("config", "config.yaml", "path to the config.yaml")
	peerPort    = flag.Int("peerPort", defaultPeerPort, "override config peerPort")
	gossipPort  = flag.Int("gossipPort", defaultGossipPort, "override config gossipPort")
	debug       = flag.Bool("debug", false, "enable verbose logging and disable ACME functions")
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

	if *debug {
		logger.Warn("ACME is disabled: no cert checking nor issuance.")
	}
	certManager.LoadAccountFromFile()
	certManager.LoadBundleFromFile()

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
		NextProtos:               []string{"multiplexer"},
	}

	gatwayTLSConfig := &tls.Config{
		Rand:                     rand.Reader,
		GetCertificate:           certManager.GetCertificatesFunc,
		ClientAuth:               tls.NoClientCert,
		NextProtos:               []string{"http/1.1", "multiplexer"},
		MinVersion:               tls.VersionTLS11,
		PreferServerCipherSuites: true,
		VerifyConnection:         checkClientSNI(bundle.Web.Domain),
	}

	peerAddr := fmt.Sprintf("%s:%d", bundle.Network.BindAddr, bundle.Multiplexer.Peer)
	gatewayAddr := fmt.Sprintf("%s:%d", bundle.Network.BindAddr, *gatewayPort)

	peerTLSListener, err := tls.Listen("tcp", peerAddr, peerTLSConfig)
	if err != nil {
		logger.Fatal("listening for peer tls connection", zap.Error(err))
	}
	defer peerTLSListener.Close()

	if bundle.Multiplexer.Peer == 0 {
		addr, _ := peerTLSListener.Addr().(*net.TCPAddr)
		peerAddr = fmt.Sprintf("%s:%d", bundle.Network.BindAddr, addr.Port)
		bundle.Multiplexer.Peer = addr.Port
	}

	peerQuicListener, err := quic.ListenAddr(peerAddr, peerTLSConfig, peer.QUICConfig())
	if err != nil {
		logger.Fatal("listening for peer quic connection", zap.Error(err))
	}
	defer peerQuicListener.Close()

	clientQuicListener, err := quic.ListenAddr(gatewayAddr, gatwayTLSConfig, peer.QUICConfig())
	if err != nil {
		logger.Fatal("listening for quic connection", zap.Error(err))
	}
	defer clientQuicListener.Close()

	gMux, err := tls.Listen("tcp", gatewayAddr, gatwayTLSConfig)
	if err != nil {
		logger.Fatal("listening for gateway connection", zap.Error(err))
	}
	defer gMux.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	alpnMux := gateway.NewALPNMux(logger, gMux)

	clientTLSListener := alpnMux.For("multiplexer")
	gatewayListener := alpnMux.For("http/1.1")

	domain := bundle.Web.Domain
	if *gatewayPort != 443 {
		domain = fmt.Sprintf("%s:%d", domain, *gatewayPort)
	}
	s, err := server.New(server.Config{
		Context:            ctx,
		Logger:             logger,
		Network:            bundle.Network,
		PeerTLSListener:    peerTLSListener,
		PeerQUICListener:   peerQuicListener,
		PeerTLSConfig:      peerTLSConfig,
		ClientTLSListener:  clientTLSListener,
		ClientQUICListener: clientQuicListener,
		Multiplexer:        bundle.Multiplexer,
		Gossip:             bundle.Gossip,
		CertManager:        certManager,
		Debug:              *debug,
		Domain:             domain,
	})
	if err != nil {
		logger.Fatal("setting up multiplexer server", zap.Error(err))
	}

	logger.Info("multiplexer peer info",
		zap.String("bindAddr", peerAddr),
		zap.String("advertiseAddr", fmt.Sprintf("%s:%d", bundle.Network.AdvertiseAddr, bundle.Multiplexer.Peer)),
		zap.Uint64("PeerID", s.PeerID()),
		zap.String("serverVersion", Version),
	)

	g, err := gateway.New(gateway.GatewayConfig{
		Logger:      logger,
		Multiplexer: s,
		Listener:    gatewayListener,
		RootDomain:  bundle.Web.Domain,
		GatewayPort: *gatewayPort,
	})
	if err != nil {
		logger.Fatal("setting up gateway server", zap.Error(err))
	}

	if err := s.Gossip(); err != nil {
		logger.Fatal("starting gossip", zap.Error(err))
	}
	s.ListenForPeers()
	s.ListenForClients()

	go gateway.RedirectHTTP(logger, bundle.Network.BindAddr, *gatewayPort)
	go g.Start(ctx)
	go profiler.StartProfiler(*profile)
	go alpnMux.Serve(ctx)

	sig := <-sigs
	logger.Info("terminating on signal", zap.String("signal", sig.String()))
}
