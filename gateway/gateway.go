package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"text/template"
	"time"

	"github.com/zllovesuki/t/profiler"
	"github.com/zllovesuki/t/server"

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
	apexServer     *apexServer
	apexAcceptor   *httpAccepter
	tunnelAcceptor *httpAccepter
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
		tunnelAcceptor: &httpAccepter{
			parent: conf.Listener,
			ch:     make(chan net.Conn, 1024),
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
	go http.Serve(g.tunnelAcceptor, g.tunnelHandler())

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
		g.Logger.Debug("tls handshake failed", zap.Error(err), zap.String("remoteAddr", conn.RemoteAddr().String()))
		conn.Close()
		profiler.GatewayRequests.WithLabelValues("error", "handshake").Add(1)
		return
	}
	conn.SetDeadline(time.Time{})

	profiler.GatewayRequests.WithLabelValues("success", "handshake").Add(1)

	cs := conn.ConnectionState()
	switch cs.ServerName {
	case g.RootDomain:
		// route to main page
		g.apexAcceptor.ch <- conn
	default:
		// maybe tunnel it
		g.tunnelAcceptor.ch <- conn
	}
}
