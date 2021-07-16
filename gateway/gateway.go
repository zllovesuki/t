package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server"
	"github.com/zllovesuki/t/shared"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type GatewayConfig struct {
	Logger      *zap.Logger
	Multiplexer *server.Server
	Listener    net.Listener
}

type Gateway struct {
	GatewayConfig
}

func New(conf GatewayConfig) *Gateway {
	return &Gateway{
		GatewayConfig: conf,
	}
}

func (g *Gateway) Start(ctx context.Context) {
	for {
		conn, err := g.Listener.Accept()
		if err != nil {
			g.Logger.Error("accepting gateway connection", zap.Error(err))
			return
		}
		tconn := conn.(*tls.Conn)
		go g.handleConnection(ctx, tconn)
	}
}

func (g *Gateway) handleConnection(ctx context.Context, conn *tls.Conn) {
	defer conn.CloseWrite()

	select {
	case <-time.After(time.Second * 5):
		g.Logger.Info("tls handshake timeout", zap.Any("remoteAddr", conn.RemoteAddr()))
		return
	default:
		err := conn.Handshake()
		if err != nil {
			g.Logger.Error("tls handshake failed", zap.Error(err))
			return
		}
	}

	xd := strings.Split(conn.ConnectionState().ServerName, ".")
	clientID := shared.PeerHash(xd[0])
	logger := g.Logger.With(zap.Uint64("ClientID", clientID))

	c, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh, err := g.Multiplexer.Forward(c, conn, multiplexer.Pair{
		Source:      g.Multiplexer.PeerID(),
		Destination: clientID,
	})
	if errors.Is(err, server.ErrDestinationNotFound) {
		WriteResponse(conn, http.StatusNotFound, "Destination not found. Propagation may take up to 2 minutes.")
		return
	}
	if err != nil {
		logger.Error("connecting", zap.Error(err))
		WriteResponse(conn, http.StatusBadGateway, "An unexpected error has occurred while attempting to forward.")
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
