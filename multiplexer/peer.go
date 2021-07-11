package multiplexer

import (
	"context"
	"fmt"
	"io"
	"net"

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
	Peer      int64
}

func NewPeer(config PeerConfig) (*Peer, error) {
	var session *yamux.Session
	var err error
	if config.Initiator {
		session, err = yamux.Client(config.Conn, nil)
		fmt.Printf("acting as client\n")
	} else {
		session, err = yamux.Server(config.Conn, nil)
		fmt.Printf("acting as server\n")
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
	go func() {
		<-ctx.Done()
		p.session.Close()
	}()
	for {
		conn, err := p.session.Accept()
		if err != nil {
			// if err == io.EOF {
			// 	return
			// }
			fmt.Printf("error accepting new stream from peer: %+v\n", err)
			return
		}
		fmt.Printf("new stream with peer %+v\n", p.config.Peer)
		go p.assembleStream(ctx, conn)
	}
}

func (p *Peer) assembleStream(c context.Context, conn net.Conn) {
	var s Pair
	buf := make([]byte, 16)

	_, err := conn.Read(buf)
	if err != nil {
		if err == io.EOF {
			return
		}
		fmt.Printf("stream read error: %+v\n", err)
		return
	}

	s.Unpack(buf)
	p.incoming <- StreamPair{
		Pair: s,
		Conn: conn,
	}
}

func (p *Peer) Peer() int64 {
	return p.config.Peer
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
	n.Write(buf)

	return n, nil
}

func (p *Peer) Bidirectional(ctx context.Context, conn net.Conn, pair Pair) {
	n, err := p.session.Open()
	if err != nil {
		fmt.Printf("error opening stream: %+v\n", err)
		return
	}

	buf := pair.Pack()
	n.Write(buf)

	Connect(ctx, n, conn)
}

func (p *Peer) Handle(ctx context.Context) <-chan StreamPair {
	return p.incoming
}

func (p *Peer) NotifyClose() <-chan int64 {
	c := make(chan int64)
	go func() {
		<-p.session.CloseChan()
		c <- p.config.Peer
		close(p.incoming)
	}()
	return c
}

func (p *Peer) Bye() error {
	fmt.Printf("bye invoked to Peer %+v\n", p.config.Peer)
	defer p.config.Conn.Close()
	return p.session.Close()
}
