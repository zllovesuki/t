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
	"time"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/gateway"
	"github.com/zllovesuki/t/multiplexer/alpn"
	"github.com/zllovesuki/t/mux"
	"github.com/zllovesuki/t/profiler"
	"github.com/zllovesuki/t/provider"
	"github.com/zllovesuki/t/server"
	"github.com/zllovesuki/t/sock"

	"github.com/TheZeroSlave/zapsentry"
	"github.com/getsentry/sentry-go"
	"github.com/lucas-clemente/quic-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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

	bundle, err := getConfig(*configPath)
	if err != nil {
		log.Fatalf("loading config file: %v", err)
	}
	if *peerPort != defaultPeerPort {
		bundle.Multiplexer.Peer = *peerPort
	}
	if *gossipPort != defaultGossipPort {
		bundle.Gossip.Port = *gossipPort
	}

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:     bundle.Sentry,
		Release: Version,
	}); err != nil {
		log.Fatalf("initializing sentry: %v", err)
	}

	if *debug {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}
	if err != nil {
		log.Fatalf("initializing zap logger: %v", err)
	}

	cfg := zapsentry.Configuration{
		EnableBreadcrumbs: true,
		BreadcrumbLevel:   zapcore.InfoLevel,
		Level:             zapcore.ErrorLevel,
		Tags: map[string]string{
			"component": "t",
		},
	}
	core, err := zapsentry.NewCore(cfg, zapsentry.NewSentryClientFromClient(sentry.CurrentHub().Client()))
	if err != nil {
		logger.Fatal("initializing zap core", zap.Error(err))
	}
	logger = zapsentry.AttachCoreToLogger(core, logger)
	defer logger.Sync()

	// redirect stdlib log output to logger, since some packages
	// are leaking log output and I have no way to redirect them
	undo, err := zap.RedirectStdLogAt(logger, zapcore.DebugLevel)
	if err != nil {
		logger.Fatal("redirecting std log", zap.Error(err))
	}
	defer undo()

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

	// we are not sure what people are using for their peer certficiates,
	// so we will included good old' non-EC ciphers
	peerTLSConfig := &tls.Config{
		Rand:                     rand.Reader,
		RootCAs:                  caCertPool,
		ClientCAs:                caCertPool,
		Certificates:             []tls.Certificate{peerCert},
		ClientAuth:               tls.RequireAndVerifyClientCert,
		VerifyConnection:         checkPeerSAN("t_Peer"),
		NextProtos:               []string{alpn.Multiplexer.String()},
		PreferServerCipherSuites: true, // kept for Go 1.16 compat
		MinVersion:               tls.VersionTLS12,
		// https://wiki.mozilla.org/Security/Server_Side_TLS (Intermediate compatibility)
		CipherSuites: []uint16{
			// TLS 1.3 ciphers
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			// TLS 1.2 ciphers
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		},
		CurvePreferences: []tls.CurveID{
			tls.X25519, tls.CurveP256, tls.CurveP384,
		},
	}

	// our acme cert generation uses ECDSA (P-256), thus we will skip
	// ciphers that do not do elliptic curve DH
	gatwayTLSConfig := &tls.Config{
		Rand:                     rand.Reader,
		GetCertificate:           certManager.GetCertificatesFunc,
		ClientAuth:               tls.NoClientCert,
		VerifyConnection:         checkClientSNI(bundle.Web.Domain),
		NextProtos:               alpn.Protos,
		PreferServerCipherSuites: true, // kept for Go 1.16 compat
		MinVersion:               tls.VersionTLS12,
		// https://wiki.mozilla.org/Security/Server_Side_TLS (Intermediate compatibility)
		CipherSuites: []uint16{
			// TLS 1.3 ciphers
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			// TLS 1.2 ciphers
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		},
		CurvePreferences: []tls.CurveID{
			tls.X25519, tls.CurveP256, tls.CurveP384,
		},
	}

	peerAddr := fmt.Sprintf("%s:%d", bundle.Network.BindAddr, bundle.Multiplexer.Peer)
	gatewayAddr := fmt.Sprintf("%s:%d", bundle.Network.BindAddr, *gatewayPort)

	defer time.Sleep(time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	qp, err := getReusePacketConn(ctx, "udp", peerAddr)
	if err != nil {
		logger.Fatal("setting up for peer quic connection", zap.Error(err))
	}
	peerQuicListener, err := quic.Listen(qp, peerTLSConfig, mux.QUICConfig())
	if err != nil {
		logger.Fatal("listening for peer quic connection", zap.Error(err))
	}
	defer peerQuicListener.Close()

	qc, err := getReusePacketConn(ctx, "udp", gatewayAddr)
	if err != nil {
		logger.Fatal("setting up for client quic connection", zap.Error(err))
	}
	clientQuicListener, err := quic.Listen(qc, gatwayTLSConfig, mux.QUICConfig())
	if err != nil {
		logger.Fatal("listening for client quic connection", zap.Error(err))
	}
	defer clientQuicListener.Close()

	p, err := getReuseListener(ctx, "tcp", peerAddr)
	if err != nil {
		logger.Fatal("listening for peer tls connection", zap.Error(err))
	}
	peerTLSListener := tls.NewListener(p, peerTLSConfig)
	defer peerTLSListener.Close()

	g, err := getReuseListener(ctx, "tcp", gatewayAddr)
	if err != nil {
		logger.Fatal("listening for gateway connection", zap.Error(err))
	}
	gMux := tls.NewListener(g, gatwayTLSConfig)
	defer gMux.Close()

	alpnMux := gateway.NewALPNMux(logger, gMux)

	clientTLSListener := alpnMux.For(alpn.Multiplexer)
	gatewayListener := alpnMux.For(alpn.Raw, alpn.HTTP, alpn.Unknown)

	if bundle.Multiplexer.Peer == 0 {
		addr, _ := peerTLSListener.Addr().(*net.TCPAddr)
		peerAddr = fmt.Sprintf("%s:%d", bundle.Network.BindAddr, addr.Port)
		bundle.Multiplexer.Peer = addr.Port
	}

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

	gw, err := gateway.New(gateway.GatewayConfig{
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
	go gw.Start(ctx)
	go profiler.StartProfiler(*profile)
	go alpnMux.Serve(ctx)

	sig := <-sigs
	logger.Info("terminating on signal", zap.String("signal", sig.String()))
}

func getReuseListener(ctx context.Context, network, address string) (net.Listener, error) {
	cfg := &net.ListenConfig{
		Control: sock.Control,
	}
	return cfg.Listen(ctx, network, address)
}

func getReusePacketConn(ctx context.Context, network, address string) (net.PacketConn, error) {
	cfg := &net.ListenConfig{
		Control: sock.Control,
	}
	return cfg.ListenPacket(ctx, network, address)
}
