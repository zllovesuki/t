package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"text/template"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/alpn"
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
	GatewayPort int
}

type Gateway struct {
	GatewayConfig
	apexServer         *apexServer
	apexAcceptor       *httpAccepter
	httpTunnelAcceptor *httpAccepter
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
			ch:     make(chan net.Conn, 1024),
		},
		httpTunnelAcceptor: &httpAccepter{
			parent: conf.Listener,
			ch:     make(chan net.Conn, 1024),
		},
		apexServer: &apexServer{
			clientPort: conf.GatewayPort,
			hostname:   conf.RootDomain,
			host:       d,
			mdTmpl:     md,
			indexTmpl:  idx,
		},
	}, nil
}

func (g *Gateway) Start(ctx context.Context) {

	go http.Serve(g.apexAcceptor, g.apexServer.Handler())
	go http.Serve(g.httpTunnelAcceptor, g.httpHandler())

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
	cs := conn.ConnectionState()
	switch cs.ServerName {
	case g.RootDomain:
		// route to main page
		g.apexAcceptor.ch <- conn
	default:
		// maybe tunnel it
		switch cs.NegotiatedProtocol {
		case alpn.Unknown.String(), alpn.HTTP.String():
			g.Logger.Debug("forward http connection")
			g.httpTunnelAcceptor.ch <- conn
		case alpn.Raw.String():
			g.Logger.Debug("forward raw connection")
			_, err := g.Multiplexer.Forward(ctx, conn, g.link(cs.ServerName, cs.NegotiatedProtocol))
			if err != nil {
				g.Logger.Error("establish raw link error", zap.Error(err))
				conn.Close()
			}
		case alpn.Multiplexer.String():
			g.Logger.Error("received alpn proposal for multiplexer on gateway")
			conn.Close()
		default:
			g.Logger.Error("unknown alpn proposal", zap.String("proposal", cs.NegotiatedProtocol))
			conn.Close()
		}
	}
}

func (g *Gateway) link(sni, proto string) multiplexer.Link {
	parts := strings.SplitN(sni, ".", 2)
	clientID := shared.PeerHash(parts[0])
	return multiplexer.Link{
		Source:      g.Multiplexer.PeerID(),
		Destination: clientID,
		ALPN:        alpn.ReverseMap[proto],
	}
}
