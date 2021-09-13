package gateway

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/zllovesuki/t/profiler"

	"go.uber.org/zap"
)

type protoListener struct {
	l net.Listener
	c chan net.Conn
	d chan struct{}
}

var _ net.Listener = &protoListener{}

func (p *protoListener) Accept() (net.Conn, error) {
	select {
	case conn, ok := <-p.c:
		if !ok {
			return nil, net.ErrClosed
		}
		return conn, nil
	case <-p.d:
		return nil, net.ErrClosed
	}
}

func (p *protoListener) Close() error {
	return p.l.Close()
}

func (p *protoListener) Addr() net.Addr {
	return p.l.Addr()
}

// ALPN is a connection muxer based on TLS extension ALPN (NextProtos)
type ALPN struct {
	logger   *zap.Logger
	listener net.Listener

	protos sync.Map
	done   chan struct{}
}

func NewALPNMux(logger *zap.Logger, listner net.Listener) *ALPN {
	return &ALPN{
		logger:   logger,
		listener: listner,
		done:     make(chan struct{}),
	}
}

func (a *ALPN) Serve(ctx context.Context) {
	defer close(a.done)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		conn, err := a.listener.Accept()
		if err != nil {
			a.logger.Error("accepting gateway connection", zap.Error(err))
			return
		}
		tconn, ok := conn.(*tls.Conn)
		if !ok {
			a.logger.Fatal("listener is not returning tls connection")
			return
		}
		go a.handshake(ctx, tconn)
	}
}

func (a *ALPN) For(protos ...string) net.Listener {
	ch := make(chan net.Conn, 32)
	for _, proto := range protos {
		a.protos.Store(proto, ch)
	}
	return &protoListener{
		l: a.listener,
		c: ch,
		d: a.done,
	}
}

func (a *ALPN) handshake(pCtx context.Context, conn *tls.Conn) {
	ctx, cancel := context.WithTimeout(pCtx, time.Second*3)
	defer cancel()

	err := conn.HandshakeContext(ctx)
	if err != nil {
		a.logger.Debug("tls handshake failed", zap.Error(err), zap.String("remoteAddr", conn.RemoteAddr().String()))
		conn.Close()
		profiler.GatewayRequests.WithLabelValues("error", "handshake").Add(1)
		return
	}

	profiler.GatewayRequests.WithLabelValues("success", "handshake").Add(1)

	cs := conn.ConnectionState()
	a.logger.Debug("tls negotiation successful", zap.Any("ConnectionState", cs))

	val, ok := a.protos.Load(cs.NegotiatedProtocol)
	if !ok {
		a.logger.Error("no listener found", zap.String("proto", cs.NegotiatedProtocol))
		conn.Close()
		return
	}
	val.(chan net.Conn) <- conn
}
