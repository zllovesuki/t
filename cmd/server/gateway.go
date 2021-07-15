package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server"
	"github.com/zllovesuki/t/shared"
	"go.uber.org/zap"
)

func Gateway(ctx context.Context, logger *zap.Logger, s *server.Server) {
	cert, err := tls.LoadX509KeyPair("tls/dev.pem", "tls/dev-key.pem")
	if err != nil {
		panic(err)
	}

	config := tls.Config{
		Certificates: []tls.Certificate{cert},
		Rand:         rand.Reader,
	}

	xd, err := tls.Listen("tcp", fmt.Sprintf("%s:%d", *ip, *webPort), &config)
	if err != nil {
		panic(err)
	}

	for {
		conn, err := xd.Accept()
		if err != nil {
			fmt.Printf("error accepting tls connection: %+v\n", err)
			return
		}
		tconn := conn.(*tls.Conn)
		go handleTLS(ctx, s, logger, tconn)
	}
}

const (
	NotFound = "HTTP/1.1 404 Not Found\r\nConnection: close\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nDestination not found. Propagation may take up to 2 minutes"
	Timeout  = "HTTP/1.1 504 Gateway Timeout\r\nConnection: close\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nDestination is taking too long to respond"
)

func handleTLS(ctx context.Context, s *server.Server, logger *zap.Logger, conn *tls.Conn) {
	var err error
	var errCh <-chan error
	defer func() {
		if errors.Is(err, server.ErrDestinationNotFound) {
			fmt.Fprint(conn, NotFound)
			conn.CloseWrite()
			return
		}
		if err != nil {
			logger.Error("connecting", zap.Error(err))
			conn.Close()
		}
	}()
	err = conn.Handshake()
	if err != nil {
		logger.Error("handshake failed", zap.Error(err))
		return
	}

	xd := strings.Split(conn.ConnectionState().ServerName, ".")
	clientID := shared.PeerHash(xd[0])
	logger = logger.With(zap.Uint64("ClientID", clientID))

	errCh, err = s.Forward(ctx, conn, multiplexer.Pair{
		Source:      s.PeerID(),
		Destination: clientID,
	})
	go func() {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errCh:
			if multiplexer.IsTimeout(err) {
				fmt.Fprint(conn, Timeout)
				conn.CloseWrite()
			}
			if !ok {
				return
			}
		}
	}()
}
