package mux

import (
	"context"
	"crypto/tls"
	"io"
	"net"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/protocol"

	multiplex "github.com/libp2p/go-mplex"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func init() {
	multiplexer.RegisterConstructor(protocol.Mplex, NewMplexPeer)
	multiplexer.RegisterDialer(protocol.Mplex, dialMplex)
}

// Mplex is a Peer implementation using libp2p's mplex
type Mplex struct {
	logger  *zap.Logger
	session *multiplex.Multiplex
	config  multiplexer.Config
	channel *multiplexer.Channel
}

var _ multiplexer.Peer = &Mplex{}

func dialMplex(addr string, t *tls.Config) (interface{}, net.Conn, func(), error) {
	return dialYamux(addr, t)
}

func NewMplexPeer(config multiplexer.Config) (multiplexer.Peer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	session := multiplex.NewMultiplex(config.Conn.(net.Conn), config.Initiator, nil)
	logger := config.Logger.With(zap.Uint64("PeerID", config.Peer), zap.Bool("Initiator", config.Initiator))

	return &Mplex{
		logger:  logger,
		session: session,
		config:  config,
		channel: multiplexer.NewChannel(),
	}, nil
}

func (p *Mplex) Start(ctx context.Context) {
	go p.channel.Run(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn, err := p.session.Accept()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					p.logger.Error("accepting stream from peers", zap.Error(err))
				}
				p.Bye()
				return
			}
			go p.streamHandshake(ctx, conn)
		}
	}
}

func (p *Mplex) Null(ctx context.Context) {
	go p.channel.Run(ctx)
	select {
	case <-ctx.Done():
		return
	default:
		_, err := p.session.Accept()
		if err != nil {
			return
		}
		p.logger.Warn("nulled Peer attempted to request a new stream")
		p.Bye()
	}
}

func (p *Mplex) Addr() net.Addr {
	return p.config.Conn.(net.Conn).RemoteAddr()
}

func (p *Mplex) Protocol() protocol.Protocol {
	return protocol.Mplex
}

type mplexConn struct {
	*multiplex.Stream
	parentConn net.Conn
}

func (m *mplexConn) LocalAddr() net.Addr {
	return m.parentConn.LocalAddr()
}

func (m *mplexConn) RemoteAddr() net.Addr {
	return m.parentConn.RemoteAddr()
}

func (p *Mplex) streamHandshake(c context.Context, conn *multiplex.Stream) {
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
		Conn: &mplexConn{
			parentConn: p.config.Conn.(net.Conn),
			Stream:     conn,
		},
	})
}

func (p *Mplex) Initiator() bool {
	return p.config.Initiator
}

func (p *Mplex) Peer() uint64 {
	return p.config.Peer
}

func (p *Mplex) Messaging(ctx context.Context) (net.Conn, error) {
	n, err := p.session.NewStream(ctx)
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
	return &mplexConn{
		Stream:     n,
		parentConn: p.config.Conn.(net.Conn),
	}, nil
}

func (p *Mplex) Direct(ctx context.Context, link multiplexer.Link) (net.Conn, error) {
	n, err := p.session.NewStream(ctx)
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

	return &mplexConn{
		Stream:     n,
		parentConn: p.config.Conn.(net.Conn),
	}, nil
}

func (p *Mplex) Bidirectional(ctx context.Context, conn net.Conn, link multiplexer.Link) (<-chan error, error) {
	n, err := p.Direct(ctx, link)
	if err != nil {
		return nil, err
	}

	errCh := multiplexer.Connect(ctx, n, conn)

	return errCh, nil
}

func (p *Mplex) Handle() <-chan multiplexer.LinkConnection {
	return p.channel.Incoming()
}

func (p *Mplex) NotifyClose() <-chan struct{} {
	return p.session.CloseChan()
}

func (p *Mplex) Bye() error {
	if p.channel.Close() {
		p.session.Close()
		p.config.Conn.(net.Conn).Close()
	}
	return nil
}
