package peer

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/protocol"

	"github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
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
		DisablePathMTUDiscovery: true,
	}
}

type QUIC struct {
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
		err = errors.Wrap(err, "opening quic session")
		return
	}
	closer = func() {
		sess.CloseWithError(quic.ApplicationErrorCode(0), "")
	}
	conn, sErr := sess.OpenStream()
	if sErr != nil {
		err = errors.Wrap(sErr, "opening quic handshake connection")
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
				p.closeTimeoutCh()
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
	return &quicConn{
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
