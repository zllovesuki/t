package mux

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/protocol"

	"github.com/libp2p/go-yamux/v2"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func init() {
	multiplexer.RegisterConstructor(protocol.Yamux, NewYamuxPeer)
	multiplexer.RegisterDialer(protocol.Yamux, dialYamux)
}

// Yamux is a Peer implementation using hashicorp's Yamux
type Yamux struct {
	logger  *zap.Logger
	session *yamux.Session
	config  multiplexer.Config
	channel *multiplexer.Channel
}

var _ multiplexer.Peer = &Yamux{}

type zapWriter struct {
	logger *zap.Logger
}

func (z *zapWriter) Write(b []byte) (int, error) {
	msg := string(b)
	switch {
	case strings.Contains(msg, "frame for missing stream"):
	case strings.Contains(msg, "iscard"): // the omission of D is intentional
	case strings.Contains(msg, "[WARN]"):
		z.logger.Warn(msg)
	default:
		z.logger.Error(msg)
	}
	return len(b), nil
}

func dialYamux(addr string, t *tls.Config) (connector interface{}, hs net.Conn, closer func(), err error) {
	conn, sErr := tls.DialWithDialer(&net.Dialer{
		Timeout: time.Second * 3,
	}, "tcp", addr, t.Clone())
	if sErr != nil {
		err = errors.Wrap(sErr, "opening tls connection")
		return
	}
	closer = func() {
		conn.Close()
	}
	connector = conn
	hs = conn
	return
}

func NewYamuxPeer(config multiplexer.Config) (multiplexer.Peer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	var session *yamux.Session
	var err error

	cfg := yamux.DefaultConfig()
	cfg.AcceptBacklog = 1024
	cfg.ReadBufSize = 0

	logger := config.Logger.With(zap.Uint64("PeerID", config.Peer), zap.Bool("Initiator", config.Initiator))
	cfg.LogOutput = &zapWriter{logger: logger}

	if config.Initiator {
		session, err = yamux.Client(config.Conn.(net.Conn), cfg)
	} else {
		session, err = yamux.Server(config.Conn.(net.Conn), cfg)
	}
	if err != nil {
		return nil, errors.Wrap(err, "starting a peer connection")
	}

	return &Yamux{
		logger:  logger,
		session: session,
		config:  config,
		channel: multiplexer.NewChannel(),
	}, nil
}

func (p *Yamux) Start(ctx context.Context) {
	go p.channel.Run(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn, err := p.session.AcceptStream()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					p.logger.Error("accepting stream from peers", zap.Error(err))
				}
				return
			}
			go p.streamHandshake(ctx, conn)
		}
	}
}

// Null will terminate the session as soon a new stream request is received. This is primarily
// used to disconnect badly behaving clients.
func (p *Yamux) Null(ctx context.Context) {
	go p.channel.Run(ctx)
	select {
	case <-ctx.Done():
		return
	default:
		_, err := p.session.AcceptStream()
		if err != nil {
			return
		}
		p.logger.Warn("nulled Peer attempted to request a new stream")
		p.Bye()
	}
}

func (p *Yamux) Addr() net.Addr {
	return p.config.Conn.(net.Conn).RemoteAddr()
}

func (p *Yamux) Protocol() protocol.Protocol {
	return protocol.Yamux
}

func (p *Yamux) streamHandshake(c context.Context, conn net.Conn) {
	var s multiplexer.Link
	buf := make([]byte, multiplexer.LinkSize)

	_, err := conn.Read(buf)
	if err != nil {
		p.logger.Error("reading stream handshake", zap.Error(err))
		return
	}

	if err := s.UnmarshalBinary(buf); err != nil {
		p.logger.Error("unmarshal link", zap.Error(err))
		return
	}

	p.channel.Put(multiplexer.LinkConnection{
		Link: s,
		Conn: conn,
	})
}

func (p *Yamux) Initiator() bool {
	return p.config.Initiator
}

func (p *Yamux) Peer() uint64 {
	return p.config.Peer
}

func (p *Yamux) Ping() (time.Duration, error) {
	return p.session.Ping()
}

func (p *Yamux) Messaging(ctx context.Context) (net.Conn, error) {
	n, err := p.session.Open(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "opening new messaging stream")
	}
	link := multiplexer.MessagingLink
	buf, err := link.MarshalBinary()
	if err != nil {
		return nil, errors.Wrap(err, "marshal link as binary")
	}

	written, err := n.Write(buf)
	if err != nil {
		return nil, errors.Wrap(err, "writing messaging stream handshake")
	}
	if written != multiplexer.LinkSize {
		return nil, errors.Errorf("invalid messaging handshake length: %d", written)
	}
	return n, nil
}

func (p *Yamux) Direct(ctx context.Context, link multiplexer.Link) (net.Conn, error) {
	n, err := p.session.Open(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "opening new stream")
	}

	buf, err := link.MarshalBinary()
	if err != nil {
		return nil, errors.Wrap(err, "marshal link as binary")
	}

	written, err := n.Write(buf)
	if err != nil {
		return nil, errors.Wrap(err, "writing stream handshake")
	}
	if written != multiplexer.LinkSize {
		return nil, errors.Errorf("invalid bidirectional handshake length: %d", written)
	}

	return n, nil
}

func (p *Yamux) Bidirectional(ctx context.Context, conn net.Conn, link multiplexer.Link) (<-chan error, error) {
	n, err := p.Direct(ctx, link)
	if err != nil {
		return nil, err
	}

	errCh := multiplexer.Connect(ctx, n, conn)

	return errCh, nil
}

func (p *Yamux) Handle() <-chan multiplexer.LinkConnection {
	return p.channel.Incoming()
}

func (p *Yamux) NotifyClose() <-chan struct{} {
	return p.session.CloseChan()
}

func (p *Yamux) Bye() error {
	if p.channel.Close() {
		p.session.Close()
		p.config.Conn.(net.Conn).Close()
	}
	return nil
}
