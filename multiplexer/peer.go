package multiplexer

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/pkg/errors"
)

type StreamPair struct {
	Conn net.Conn
	Pair Pair
}

type Peer struct {
	session  *yamux.Session
	config   PeerConfig
	incoming chan StreamPair
}

type PeerConfig struct {
	Conn      net.Conn
	Initiator bool
	Peer      uint64
}

func NewPeer(config PeerConfig) (*Peer, error) {
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
		session:  session,
		config:   config,
		incoming: make(chan StreamPair, 5),
	}, nil
}

func (p *Peer) Start(ctx context.Context) {
	for {
		conn, err := p.session.AcceptStream()
		if err != nil {
			return
		}
		go p.streamHandshake(ctx, conn)
	}
}

func (p *Peer) streamHandshake(c context.Context, conn net.Conn) {
	var s Pair
	buf := make([]byte, 16)

	_, err := conn.Read(buf)
	if err != nil {
		fmt.Printf("error when reading stream handshake: %+v\n", err)
		return
	}

	s.Unpack(buf)
	p.incoming <- StreamPair{
		Pair: s,
		Conn: conn,
	}
}

func (p *Peer) Peer() uint64 {
	return p.config.Peer
}

func (p *Peer) Ping() (time.Duration, error) {
	return p.session.Ping()
}

func (p *Peer) OpenNotify() (net.Conn, error) {
	n, err := p.session.Open()
	if err != nil {
		return nil, errors.Wrap(err, "opening stream")
	}

	pair := Pair{
		Source:      0,
		Destination: 0,
	}
	buf := pair.Pack()
	if _, err := n.Write(buf); err != nil {
		fmt.Printf("error when writing stream handshake: %+v\n", err)
		return nil, err
	}

	return n, nil
}

func (p *Peer) Bidirectional(ctx context.Context, conn net.Conn, pair Pair) {
	n, err := p.session.Open()
	if err != nil {
		return
	}

	buf := pair.Pack()
	n.Write(buf)

	Connect(ctx, n, conn)
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
