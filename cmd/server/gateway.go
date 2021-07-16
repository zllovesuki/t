package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/pkg/errors"
	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server"
	"github.com/zllovesuki/t/shared"
	"go.uber.org/zap"
)

func Gateway(ctx context.Context, logger *zap.Logger, s *server.Server, bundle *ConfigBundle) {
	cert, err := tls.LoadX509KeyPair(bundle.TLS.Client.Cert, bundle.TLS.Client.Key)
	if err != nil {
		panic(err)
	}

	config := tls.Config{
		Certificates:             []tls.Certificate{cert},
		Rand:                     rand.Reader,
		NextProtos:               []string{"http/1.1"},
		PreferServerCipherSuites: true,
	}

	xd, err := tls.Listen("tcp", fmt.Sprintf("%s:%d", bundle.Multiplexer.Addr, *webPort), &config)
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

func WriteResponse(w net.Conn, statusCode int, message string) {
	text := http.StatusText(statusCode)
	w.Write([]byte("HTTP/1.1 "))
	w.Write([]byte(fmt.Sprint(statusCode)))
	w.Write([]byte(" "))
	w.Write([]byte(text))
	w.Write([]byte("\r\nContent-Type: text/plain; charset=utf-8"))
	w.Write([]byte("\r\nConnection: close\r\n\r\n"))
	w.Write([]byte(message))
}

func handleTLS(ctx context.Context, s *server.Server, logger *zap.Logger, conn *tls.Conn) {
	defer conn.CloseWrite()

	err := conn.Handshake()
	if err != nil {
		logger.Error("handshake failed", zap.Error(err))
		return
	}

	if !strings.HasSuffix(conn.ConnectionState().ServerName, "dev.n45.net") {
		WriteResponse(conn, http.StatusForbidden, "Unauthroized peering server name")
		return
	}
	xd := strings.Split(conn.ConnectionState().ServerName, ".")
	clientID := shared.PeerHash(xd[0])
	logger = logger.With(zap.Uint64("ClientID", clientID))

	c, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh, err := s.Forward(c, conn, multiplexer.Pair{
		Source:      s.PeerID(),
		Destination: clientID,
	})
	if errors.Is(err, server.ErrDestinationNotFound) {
		WriteResponse(conn, http.StatusNotFound, "Destination not found. Propagation may take up to 2 minutes.")
		return
	}
	if err != nil {
		logger.Error("connecting", zap.Error(err))
		WriteResponse(conn, http.StatusBadGateway, "An unexpected error has occurred while attempt to forward.")
		return
	}
	sent := false
	for {
		select {
		case <-c.Done():
			return
		case err, ok := <-errCh:
			if !sent && multiplexer.IsTimeout(err) {
				sent = true
				WriteResponse(conn, http.StatusGatewayTimeout, "Destination is taking too long to respond.")
				return
			}
			if !ok {
				return
			}
		}
	}
}
