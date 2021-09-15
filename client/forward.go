package client

import (
	"context"
	"net"
	"net/url"
	"os"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/sock"

	"go.uber.org/zap"
)

type ForwardOpts struct {
	Logger *zap.Logger
	URL    string
	Addr   string
	Debug  bool
	Sigs   chan os.Signal
}

func Forward(ctx context.Context, opts ForwardOpts) {
	logger := opts.Logger

	u, err := url.Parse(opts.URL)
	if err != nil {
		logger.Fatal("parsing gateway target", zap.Error(err))
	}

	switch u.Scheme {
	case "https":
	default:
		logger.Fatal("unsupported scheme. valid schemes: https", zap.String("schema", u.Scheme))
	}

	x, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg := &net.ListenConfig{
		Control: sock.Control,
	}
	l, err := cfg.Listen(x, "tcp", opts.Addr)
	if err != nil {
		logger.Fatal("listening for connections", zap.Error(err))
	}

	logger.Info("forwarding connections via gateway", zap.String("URL", opts.URL), zap.String("Listen", opts.Addr))

	go func() {
		<-opts.Sigs
		l.Close()
		cancel()
	}()

	for {
		lConn, err := l.Accept()
		if err != nil {
			logger.Fatal("accepting tcp connections", zap.Error(err))
		}
		go func() {
			rConn, err := getConn(opts.Debug, u)
			if err != nil {
				logger.Fatal("connecting to gateway", zap.Error(err))
			}
			logger.Info("new connection", zap.String("RemoteAddr", lConn.RemoteAddr().String()))
			multiplexer.Connect(x, lConn, rConn)
		}()
	}
}
