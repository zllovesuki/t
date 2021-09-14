package client

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"time"

	"github.com/zllovesuki/t/multiplexer/alpn"

	"go.uber.org/zap"
)

const (
	DefaultConnect = "https://exhaust-timing-harmless-saved-startup.tunnel.example.com"
)

type ConnectOpts struct {
	Logger *zap.Logger
	URL    string
	Debug  bool
	Sigs   chan os.Signal
}

func Connect(ctx context.Context, opts ConnectOpts) {
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

	conn, err := getConn(opts.Debug, u)
	if err != nil {
		opts.Logger.Fatal("connecting to gateway", zap.Error(err))
	}

	go func() {
		_, err := io.Copy(os.Stdout, conn)
		if err != nil {
			logger.Error("error piping to target", zap.Error(err))
			opts.Sigs <- syscall.SIGTERM
		}
	}()
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		if err != nil {
			logger.Error("error piping to target", zap.Error(err))
			opts.Sigs <- syscall.SIGTERM
		}
	}()

	<-opts.Sigs
}

func getConn(debug bool, u *url.URL) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout: time.Second * 3,
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return tls.DialWithDialer(dialer, "tcp", fmt.Sprintf("%s:%s", u.Hostname(), port), &tls.Config{
		Rand:               rand.Reader,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: debug,
		NextProtos:         []string{alpn.Raw.String()},
	})
}
