package peer

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/multiplexer"

	"github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func init() {
	multiplexer.Register(multiplexer.QUICProtocol, NewQuicPeer)
}

type QUIC struct {
	logger    *zap.Logger
	session   quic.Session
	config    multiplexer.Config
	incoming  chan multiplexer.LinkConnection
	closed    *int32
	timeoutCh chan struct{}
}

var _ multiplexer.Peer = &QUIC{}

func NewQuicPeer(config multiplexer.Config) (multiplexer.Peer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &QUIC{
		logger:    config.Logger,
		session:   config.Conn.(quic.Session),
		config:    config,
		incoming:  make(chan multiplexer.LinkConnection, 32),
		closed:    new(int32),
		timeoutCh: make(chan struct{}),
	}, nil
}

func (p *QUIC) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn, err := p.session.AcceptStream(p.session.Context())
			if err != nil {
				if !errors.Is(err, io.EOF) {
					p.logger.Error("accepting stream from peers", zap.Error(err))
				}
				close(p.timeoutCh)
				return
			}
			go p.streamHandshake(ctx, conn)
		}
	}
}

func (p *QUIC) Null(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	default:
		_, err := p.session.AcceptStream(p.session.Context())
		if err != nil {
			close(p.timeoutCh)
			return
		}
		p.logger.Warn("nulled Peer attempted to request a new stream")
		p.Bye()
	}
}

func (p *QUIC) Addr() net.Addr {
	return p.config.Conn.(quic.Session).RemoteAddr()
}

type QuicConn struct {
	quic.Stream
	Session quic.Session
}

func (m *QuicConn) LocalAddr() net.Addr {
	return m.Session.LocalAddr()
}

func (m *QuicConn) RemoteAddr() net.Addr {
	return m.Session.RemoteAddr()
}

func (p *QUIC) streamHandshake(c context.Context, conn quic.Stream) {
	var s multiplexer.Link
	buf := make([]byte, multiplexer.LinkSize)

	read, err := conn.Read(buf)
	if err != nil {
		p.logger.Error("reading stream handshake", zap.Error(err))
		return
	}
	if read != multiplexer.LinkSize {
		p.logger.Error("invalid handshake length", zap.Int("length", read))
		return
	}

	s.Unpack(buf)
	p.incoming <- multiplexer.LinkConnection{
		Link: s,
		Conn: &QuicConn{
			Stream:  conn,
			Session: p.session,
		},
	}
}

func (p *QUIC) Initiator() bool {
	return p.config.Initiator
}

func (p *QUIC) Peer() uint64 {
	return p.config.Peer
}

func (p *QUIC) Ping() (time.Duration, error) {
	// TODO(zllovesuki): fill this stub
	return 0, nil
}

func (p *QUIC) Messaging(ctx context.Context) (net.Conn, error) {
	n, err := p.session.OpenStream()
	if err != nil {
		return nil, errors.Wrap(err, "opening new messaging stream")
	}
	link := multiplexer.MessagingLink
	buf := link.Pack()
	written, err := n.Write(buf)
	if err != nil {
		return nil, errors.Wrap(err, "writing messaging stream handshake")
	}
	if written != multiplexer.LinkSize {
		return nil, errors.Errorf("invalid messaging handshake length: %d", written)
	}
	return &QuicConn{
		Stream:  n,
		Session: p.session,
	}, nil
}

func (p *QUIC) Direct(ctx context.Context, link multiplexer.Link) (net.Conn, error) {
	n, err := p.session.OpenStream()
	if err != nil {
		return nil, errors.Wrap(err, "opening new stream")
	}

	buf := link.Pack()
	written, err := n.Write(buf)
	if err != nil {
		return nil, errors.Wrap(err, "writing stream handshake")
	}
	if written != multiplexer.LinkSize {
		return nil, errors.Errorf("invalid bidirectional handshake length: %d", written)
	}

	return &QuicConn{
		Stream:  n,
		Session: p.session,
	}, nil
}

func (p *QUIC) Bidirectional(ctx context.Context, conn net.Conn, link multiplexer.Link) (<-chan error, error) {
	n, err := p.Direct(ctx, link)
	if err != nil {
		return nil, err
	}

	errCh := multiplexer.Connect(ctx, n, conn)

	return errCh, nil
}

func (p *QUIC) Handle() <-chan multiplexer.LinkConnection {
	return p.incoming
}

func (p *QUIC) NotifyClose() <-chan struct{} {
	return p.timeoutCh
}

func (p *QUIC) Bye() error {
	if atomic.CompareAndSwapInt32(p.closed, 0, 1) {
		close(p.incoming)
		p.session.CloseWithError(quic.ApplicationErrorCode(1), "peer is closing")
	}
	return nil
}
