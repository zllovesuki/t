package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"text/template"
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
	RootDomain  string
	ClientPort  int
	GatewayPort int
}

type Gateway struct {
	GatewayConfig
	apexAcceptor *httpAccepter
	apexServer   *apexServer
}

func New(conf GatewayConfig) (*Gateway, error) {
	md, err := template.New("content").Parse(tmpl)
	if err != nil {
		return nil, errors.Wrap(err, "reading markdown for apex template")
	}
	idx, err := template.New("index").Parse(index)
	if err != nil {
		return nil, errors.Wrap(err, "reading index for apex template")
	}
	d := conf.RootDomain
	if conf.GatewayPort != 443 {
		d = fmt.Sprintf("%s:%d", d, conf.GatewayPort)
	}
	return &Gateway{
		GatewayConfig: conf,
		apexAcceptor: &httpAccepter{
			parent: conf.Listener,
			ch:     make(chan net.Conn),
		},
		apexServer: &apexServer{
			clientPort: conf.ClientPort,
			hostname:   conf.RootDomain,
			host:       d,
			mdTmpl:     md,
			indexTmpl:  idx,
		},
	}, nil
}

func (g *Gateway) Start(ctx context.Context) {

	go http.Serve(g.apexAcceptor, g.apexServer.Handler())

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
	conn.SetDeadline(time.Now().Add(time.Second * 5))
	err := conn.Handshake()
	if err != nil {
		g.Logger.Error("tls handshake failed", zap.Error(err))
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})

	var rerouted bool
	defer func() {
		if rerouted {
			return
		}
		conn.CloseWrite()
	}()

	cs := conn.ConnectionState()
	switch cs.ServerName {
	case g.RootDomain:
		// route to main page
		rerouted = true
		g.apexAcceptor.ch <- conn
		return
	default:
	}

	xd := strings.Split(cs.ServerName, ".")
	clientID := shared.PeerHash(xd[0])
	logger := g.Logger.With(zap.Uint64("ClientID", clientID))

	c, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh, err := g.Multiplexer.Forward(c, conn, multiplexer.Link{
		Source:      g.Multiplexer.PeerID(),
		Destination: clientID,
	})
	if errors.Is(err, server.ErrDestinationNotFound) {
		WriteResponse(conn, http.StatusNotFound, "Destination not found. Propagation may take up to 2 minutes.")
		return
	}
	if err != nil {
		logger.Error("connecting to peer", zap.Error(err))
		WriteResponse(conn, http.StatusBadGateway, "An unexpected error has occurred while attempting to forward.")
		return
	}
	for {
		select {
		case <-c.Done():
			return
		case err, ok := <-errCh:
			if multiplexer.IsTimeout(err) {
				continue
			}
			if err != nil {
				logger.Error("forwarding connection", zap.Error(err))
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
