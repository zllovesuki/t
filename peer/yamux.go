package peer

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/multiplexer"

	"github.com/hashicorp/yamux"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// Yamux is a Peer implementation using hashicorp's Yamux
type Yamux struct {
	logger   *zap.Logger
	session  *yamux.Session
	config   YamuxConfig
	incoming chan multiplexer.LinkConnection
	closed   *int32
}

var _ multiplexer.Peer = &Yamux{}

type YamuxConfig struct {
	Logger    *zap.Logger
	Conn      net.Conn
	Initiator bool
	Peer      uint64
}

func (p *YamuxConfig) validate() error {
	if p.Logger == nil {
		return errors.New("nil logger is invalid")
	}
	if p.Conn == nil {
		return errors.New("nil conn is invalid")
	}
	if p.Peer == 0 {
		return errors.New("peer cannot be 0")
	}
	return nil
}

func NewYamuxPeer(config YamuxConfig) (*Yamux, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	var session *yamux.Session
	var err error
	if config.Initiator {
		session, err = yamux.Client(config.Conn, nil)
	} else {
		session, err = yamux.Server(config.Conn, nil)
	}
	if err != nil {
		return nil, errors.Wrap(err, "starting a peer connection")
	}

	return &Yamux{
		logger:   config.Logger.With(zap.Uint64("PeerID", config.Peer), zap.Bool("Initiator", config.Initiator)),
		session:  session,
		config:   config,
		incoming: make(chan multiplexer.LinkConnection, 32),
		closed:   new(int32),
	}, nil
}

func (p *Yamux) Start(ctx context.Context) {
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
	return p.config.Conn.RemoteAddr()
}

func (p *Yamux) streamHandshake(c context.Context, conn net.Conn) {
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
		Conn: conn,
	}
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

func (p *Yamux) Messaging() (net.Conn, error) {
	n, err := p.session.Open()
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
	return n, nil
}

func (p *Yamux) Direct(ctx context.Context, link multiplexer.Link) (net.Conn, error) {
	n, err := p.session.Open()
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

	go func() {
		<-ctx.Done()
		n.Close()
	}()

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
	return p.incoming
}

func (p *Yamux) NotifyClose() <-chan struct{} {
	return p.session.CloseChan()
}

func (p *Yamux) Bye() error {
	if atomic.CompareAndSwapInt32(p.closed, 0, 1) {
		close(p.incoming)
		p.session.Close()
		p.config.Conn.Close()
	}
	return nil
}
