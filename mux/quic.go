package mux

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/protocol"

	"github.com/lucas-clemente/quic-go"
	"go.uber.org/zap"
)

func init() {
	multiplexer.RegisterConstructor(protocol.QUIC, NewQuicPeer)
	multiplexer.RegisterDialer(protocol.QUIC, dialQuic)
}

func quicConfigCommon() *quic.Config {
	return &quic.Config{
		KeepAlive:               true,
		HandshakeIdleTimeout:    time.Second * 3,
		MaxIdleTimeout:          time.Second * 15,
		DisablePathMTUDiscovery: false,
	}
}

type QUIC struct {
	_               uint64 // for sync/atomic
	logger          *zap.Logger
	session         quic.Session
	config          multiplexer.Config
	channel         *multiplexer.Channel
	timeoutCh       chan struct{}
	timeoutChClosed *int32
}

var _ multiplexer.Peer = &QUIC{}

func dialQuic(addr string, t *tls.Config) (connector interface{}, hs net.Conn, closer func(), err error) {
	var sess quic.Session
	sess, err = quic.DialAddr(addr, t.Clone(), QUICConfig())
	if err != nil {
		err = fmt.Errorf("opening quic session: %w", err)
		return
	}
	closer = func() {
		sess.CloseWithError(quic.ApplicationErrorCode(0), "")
	}
	conn, sErr := sess.OpenStream()
	if sErr != nil {
		err = fmt.Errorf("opening quic handshake stream: %w", sErr)
		return
	}
	connector = sess
	hs = WrapQUIC(sess, conn)
	return
}

func NewQuicPeer(config multiplexer.Config) (multiplexer.Peer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &QUIC{
		logger:          config.Logger,
		session:         config.Conn.(quic.Session),
		config:          config,
		channel:         multiplexer.NewChannel(),
		timeoutCh:       make(chan struct{}),
		timeoutChClosed: new(int32),
	}, nil
}

func (p *QUIC) Start(ctx context.Context) {
	go p.channel.Run(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn, err := p.session.AcceptStream(ctx)
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

func (p *QUIC) Null(ctx context.Context) {
	go p.channel.Run(ctx)
	select {
	case <-ctx.Done():
		return
	default:
		_, err := p.session.AcceptStream(ctx)
		if err != nil {
			p.closeTimeoutCh()
			return
		}
		p.logger.Warn("nulled Peer attempted to request a new stream")
		p.Bye()
	}
}

func (p *QUIC) Addr() net.Addr {
	return p.config.Conn.(quic.Session).RemoteAddr()
}

func (p *QUIC) Protocol() protocol.Protocol {
	return protocol.QUIC
}

type quicConn struct {
	quic.Stream
	Session quic.Session
}

func (m *quicConn) LocalAddr() net.Addr {
	return m.Session.LocalAddr()
}

func (m *quicConn) RemoteAddr() net.Addr {
	return m.Session.RemoteAddr()
}

func (p *QUIC) streamHandshake(c context.Context, conn quic.Stream) {
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
		Conn: &quicConn{
			Stream:  conn,
			Session: p.session,
		},
	})
}

func (p *QUIC) Initiator() bool {
	return p.config.Initiator
}

func (p *QUIC) Peer() uint64 {
	return p.config.Peer
}

func (p *QUIC) Messaging(ctx context.Context) (net.Conn, error) {
	n, err := p.session.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("opening new messaging stream: %w", err)
	}
	if err := messagingHandshake(n); err != nil {
		return nil, err
	}
	return &quicConn{
		Stream:  n,
		Session: p.session,
	}, nil
}

func (p *QUIC) Direct(ctx context.Context, link multiplexer.Link) (net.Conn, error) {
	n, err := p.session.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("opening new stream: %w", err)
	}
	if err := directHandshake(link, n); err != nil {
		return nil, err
	}
	return &quicConn{
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
	return p.channel.Incoming()
}

func (p *QUIC) closeTimeoutCh() {
	if atomic.CompareAndSwapInt32(p.timeoutChClosed, 0, 1) {
		close(p.timeoutCh)
	}
}

func (p *QUIC) NotifyClose() <-chan struct{} {
	return p.timeoutCh
}

func (p *QUIC) Bye() error {
	if p.channel.Close() {
		p.session.CloseWithError(quic.ApplicationErrorCode(1), "peer is closing")
		p.closeTimeoutCh()
	}
	return nil
}
