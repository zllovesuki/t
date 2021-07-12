package server

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"
	"go.uber.org/zap"

	"github.com/pkg/errors"
)

var (
	ErrEndpointNotFound = fmt.Errorf("not peer is registered with this endpoint")
)

type Server struct {
	parentCtx        context.Context
	id               uint64
	meta             Meta
	peers            *state.PeerMap
	clients          *state.PeerMap
	remoteClients    *state.ClientMap
	notifier         *state.Notifier
	peerListner      net.Listener
	clientListener   net.Listener
	logger           *zap.Logger
	gossip           *memberlist.Memberlist
	gossipCfg        *memberlist.Config
	config           Config
	peerConnectQueue chan Meta
}

type Config struct {
	Logger         *zap.Logger
	PeerListener   net.Listener
	ClientListener net.Listener
	Gossip         struct {
		IP      string
		Port    int
		Members []string
	}
}

func New(conf Config) (*Server, error) {
	rand.Seed(time.Now().UnixNano())

	self := rand.Uint64()
	addr := conf.PeerListener.Addr().(*net.TCPAddr)

	pMap := state.NewPeerMap(conf.Logger, self)
	cMap := state.NewPeerMap(conf.Logger, self)
	rcMap := state.NewClientMap(self)

	s := &Server{
		meta: Meta{
			PeerID:      self,
			Multiplexer: fmt.Sprintf("%s:%d", addr.IP, addr.Port),
		},
		peers:            pMap,
		clients:          cMap,
		remoteClients:    rcMap,
		notifier:         state.NewNotifer(),
		id:               self,
		peerListner:      conf.PeerListener,
		clientListener:   conf.ClientListener,
		logger:           conf.Logger,
		config:           conf,
		peerConnectQueue: make(chan Meta, 16),
	}

	c := memberlist.DefaultWANConfig()
	c.AdvertiseAddr = conf.Gossip.IP
	c.AdvertisePort = conf.Gossip.Port
	c.BindAddr = conf.Gossip.IP
	c.BindPort = conf.Gossip.Port
	c.Events = s
	c.Delegate = s
	c.Name = fmt.Sprint(self)
	c.LogOutput = io.Discard

	s.gossipCfg = c

	return s, nil
}

func (s *Server) Start(ctx context.Context) {
	s.parentCtx = ctx
	// go func() {
	// 	for {
	// 		<-time.After(time.Second * 10)
	// 		fmt.Printf("%d\n", s.PeerID())
	// 		s.peers.Print()
	// 		s.clients.Print()
	// 		s.remoteClients.Print()
	// 	}
	// }()
	go func() {
		for {
			conn, err := s.peerListner.Accept()
			// fmt.Printf("new connection\n")
			if err != nil {
				s.logger.Error("accepting TCP connection", zap.Error(err))
				return
			}
			go s.peerHandshake(ctx, conn)
		}
	}()
	go func() {
		for {
			conn, err := s.clientListener.Accept()
			// fmt.Printf("new connection\n")
			if err != nil {
				s.logger.Error("accepting TCP connection", zap.Error(err))
				return
			}
			go s.clientHandshake(ctx, conn)
		}
	}()
	go s.handleClientEvents(ctx)
	go s.handlePeerEvents(ctx)
	go s.handlePeerConnects(ctx)
}

func (s *Server) PeerID() uint64 {
	return s.id
}

func (s *Server) Meta() []byte {
	return s.meta.Pack()
}

func (s *Server) clientHandshake(ctx context.Context, conn net.Conn) {
	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		logger.Error("error during client handshake", zap.Error(err))
		conn.Close()
	}()

	var pair multiplexer.Pair
	r := make([]byte, 16)

	conn.SetReadDeadline(time.Now().Add(time.Second * 3))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 3))
	_, err = conn.Read(r)
	if err != nil {
		err = errors.Wrap(err, "reading handshake")
		return
	}
	pair.Unpack(r)

	logger = logger.With(zap.Uint64("peerID", pair.Source))

	logger.Info("handshake with client", zap.Any("peer", pair))

	if pair.Source == 0 || pair.Source == pair.Destination {
		err = errors.Errorf("invalid client handshake %+v", pair)
		return
	}
	if pair.Source == s.PeerID() {
		err = errors.Errorf("loop: connecting to ourself %+v", pair)
		return
	}

	pair.Destination = s.PeerID()
	w := pair.Pack()
	_, err = conn.Write(w)
	if err != nil {
		err = errors.Wrap(err, "replying handshake")
		return
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	err = s.clients.NewPeer(ctx, state.PeerConfig{
		Conn:      conn,
		Peer:      pair.Source,
		Initiator: false,
		Wait:      time.Second * 1,
	})
	if err != nil {
		err = errors.Wrap(err, "setting up client")
		return
	}

	s.clients.Print()
}

func (s *Server) peerHandshake(ctx context.Context, conn net.Conn) {
	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		logger.Error("error during peer handshake", zap.Error(err))
		// conn.Close()
	}()

	var pair multiplexer.Pair
	r := make([]byte, 16)

	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 5))
	_, err = conn.Read(r)
	if err != nil {
		err = errors.Wrap(err, "reading handshake")
		return
	}
	pair.Unpack(r)

	logger = logger.With(zap.Uint64("peerID", pair.Source))

	logger.Info("handshake with peer", zap.Any("peer", pair))

	if (pair.Source == 0 || pair.Destination == 0) || (pair.Source == pair.Destination) {
		err = errors.Errorf("invalid peer handshake %+v", pair)
		return
	}
	if pair.Source == s.PeerID() {
		err = errors.Errorf("loop: connecting to ourself %+v", pair)
		return
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	err = s.peers.NewPeer(ctx, state.PeerConfig{
		Conn:      conn,
		Peer:      pair.Source,
		Initiator: false,
		Wait:      time.Second,
	})
	if err != nil {
		err = errors.Wrap(err, "setting up peer")
		return
	}

	s.peers.Print()
}

func (s *Server) findSession(pair multiplexer.Pair) *multiplexer.Peer {
	// is the client connected locally?
	p := s.clients.Get(pair.Destination)
	// can we find a peer that has the client?
	if p == nil {
		peer := s.remoteClients.GetPeer(pair.Destination)
		p = s.peers.Get(peer)
	}
	return p
}

func (s *Server) Open(ctx context.Context, conn net.Conn, pair multiplexer.Pair) error {
	p := s.findSession(pair)
	if p == nil {
		return errors.Errorf("destination %d not found among peers", pair.Destination)
	}
	s.logger.Info("forwarding stream", zap.Any("peer", pair))
	go p.Bidirectional(ctx, conn, pair)
	return nil
}
