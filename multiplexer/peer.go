package multiplexer

import (
	"context"
	"net"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type StreamPair struct {
	Conn net.Conn
	Pair Pair
}

type Peer struct {
	logger   *zap.Logger
	session  *yamux.Session
	config   PeerConfig
	incoming chan StreamPair
}

type PeerConfig struct {
	Logger    *zap.Logger
	Conn      net.Conn
	Initiator bool
	Peer      uint64
}

func (p *PeerConfig) validate() error {
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

func NewPeer(config PeerConfig) (*Peer, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	var session *yamux.Session
	var err error
	if config.Initiator {
		session, err = yamux.Client(config.Conn, nil)
		// fmt.Printf("acting as client\n")
	} else {
		session, err = yamux.Server(config.Conn, nil)
		// fmt.Printf("acting as server\n")
	}
	if err != nil {
		return nil, errors.Wrap(err, "starting a peer connection")
	}

	return &Peer{
		logger:   config.Logger.With(zap.Uint64("PeerID", config.Peer), zap.Bool("Initiator", config.Initiator)),
		session:  session,
		config:   config,
		incoming: make(chan StreamPair, 32),
	}, nil
}

func (p *Peer) Start(ctx context.Context) {
	for {
		conn, err := p.session.AcceptStream()
		if err != nil {
			p.logger.Error("accepting stream from peers", zap.Error(err))
			return
		}
		go p.streamHandshake(ctx, conn)
	}
}

func (p *Peer) streamHandshake(c context.Context, conn net.Conn) {
	var s Pair
	buf := make([]byte, PairSize)

	read, err := conn.Read(buf)
	if err != nil {
		p.logger.Error("reading stream handshake", zap.Error(err))
		return
	}
	if read != PairSize {
		p.logger.Error("invalid handshake length", zap.Int("length", read))
		return
	}

	s.Unpack(buf)
	p.incoming <- StreamPair{
		Pair: s,
		Conn: conn,
	}
}

func (p *Peer) Initiator() bool {
	return p.config.Initiator
}

func (p *Peer) Peer() uint64 {
	return p.config.Peer
}

func (p *Peer) Ping() (time.Duration, error) {
	return p.session.Ping()
}

func (p *Peer) Bidirectional(ctx context.Context, conn net.Conn, pair Pair) (<-chan error, error) {
	n, err := p.session.Open()
	if err != nil {
		return nil, errors.Wrap(err, "opening new stream")
	}

	buf := pair.Pack()
	written, err := n.Write(buf)
	if err != nil {
		return nil, errors.Wrap(err, "writing stream handshake")
	}
	if written != PairSize {
		return nil, errors.Errorf("invalid bidirectional handshake length: %d", written)
	}

	errCh := Connect(ctx, n, conn)

	return errCh, nil
}

func (p *Peer) Handle(ctx context.Context) <-chan StreamPair {
	return p.incoming
}

func (p *Peer) NotifyClose() <-chan struct{} {
	return p.session.CloseChan()
}

func (p *Peer) Bye() error {
	defer p.config.Conn.Close()
	close(p.incoming)
	return p.session.Close()
}
