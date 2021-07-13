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
	parentCtx      context.Context
	config         Config
	id             uint64
	meta           Meta
	peers          *state.PeerMap
	clients        *state.PeerMap
	peerGraph      *state.PeerGraph
	peerListner    net.Listener
	clientListener net.Listener
	logger         *zap.Logger
	gossip         *memberlist.Memberlist
	gossipCfg      *memberlist.Config
	broadcasts     *memberlist.TransmitLimitedQueue
	updatesCh      chan state.ClientUpdate
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
	pg := state.NewPeerGraph(self)

	s := &Server{
		meta: Meta{
			PeerID:      self,
			Multiplexer: fmt.Sprintf("%s:%d", addr.IP, addr.Port),
		},
		config:         conf,
		peers:          pMap,
		clients:        cMap,
		peerGraph:      pg,
		id:             self,
		peerListner:    conf.PeerListener,
		clientListener: conf.ClientListener,
		logger:         conf.Logger,
		updatesCh:      make(chan state.ClientUpdate, 16),
		broadcasts: &memberlist.TransmitLimitedQueue{
			NumNodes: func() int {
				// include ourself
				return pMap.Len() + 1
			},
			RetransmitMult: 1,
		},
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
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-time.After(time.Second * 15):
				fmt.Printf("peerGraph at time %s:\n%+v\n", t.Format(time.RFC3339), s.peerGraph)
			}
		}
	}()
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
	go s.handleNotify(ctx)
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
	var read int
	r := make([]byte, multiplexer.PairSize)

	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 5))
	read, err = conn.Read(r)
	if err != nil {
		err = errors.Wrap(err, "reading handshake")
		return
	}
	if read != multiplexer.PairSize {
		err = errors.Errorf("invalid handshake length received from client: %d", read)
		return
	}
	pair.Unpack(r)

	logger = logger.With(zap.Uint64("peerID", pair.Source))

	logger.Info("handshake with client", zap.Any("peer", pair))

	if pair.Source == 0 ||
		pair.Source == pair.Destination ||
		pair.Destination != 0 ||
		pair.Source == s.PeerID() {
		err = errors.Errorf("invalid client handshake %+v", pair)
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
		Wait:      time.Second,
	})
	if err != nil {
		err = errors.Wrap(err, "setting up client")
	}
}

func (s *Server) peerHandshake(ctx context.Context, conn net.Conn) {
	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		logger.Error("error during peer handshake", zap.Error(err))
		conn.Close()
	}()

	var pair multiplexer.Pair
	var read int
	r := make([]byte, multiplexer.PairSize)

	conn.SetReadDeadline(time.Now().Add(time.Second * 10))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 10))
	read, err = conn.Read(r)
	if err != nil {
		err = errors.Wrap(err, "reading handshake")
		return
	}
	if read != multiplexer.PairSize {
		err = errors.Errorf("invalid handshake length received from peer: %d", read)
	}
	pair.Unpack(r)

	logger = logger.With(zap.Uint64("peerID", pair.Source))

	logger.Info("handshake with peer", zap.Any("peer", pair))

	if (pair.Source == 0 || pair.Destination == 0) ||
		(pair.Source == pair.Destination) ||
		(pair.Destination != s.PeerID()) ||
		pair.Source == s.PeerID() {
		err = errors.Errorf("invalid peer handshake %+v", pair)
		return
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	err = s.peers.NewPeer(ctx, state.PeerConfig{
		Conn:      conn,
		Peer:      pair.Source,
		Initiator: false,
		Wait:      time.Second * time.Duration(rand.Intn(3)+1),
	})
	if err != nil {
		err = errors.Wrap(err, "setting up peer")
	}
}

func (s *Server) findPath(pair multiplexer.Pair) *multiplexer.Peer {
	// TODO(zllovesuki): Make the walk more efficient
	clientPeers := s.peerGraph.GetEdges(pair.Destination)
	if len(clientPeers) == 0 {
		return nil
	}
	for _, clientPeer := range clientPeers {
		// is the client connected locally?
		if clientPeer == s.PeerID() {
			return s.clients.Get(pair.Destination)
		}
		// can we find a peer that the client is connected to?
		ourPeers := s.peerGraph.GetEdges(s.PeerID())
		for _, ourPeer := range ourPeers {
			if clientPeer != ourPeer {
				continue
			}
			return s.peers.Get(ourPeer)
		}
	}
	return nil
}

func (s *Server) Forward(ctx context.Context, conn net.Conn, pair multiplexer.Pair) error {
	p := s.findPath(pair)
	if p == nil {
		return errors.Errorf("destination %d not found among peers", pair.Destination)
	}
	errCh, err := p.Bidirectional(ctx, conn, pair)
	if err != nil {
		return err
	}
	go func(ch <-chan error) {
		for e := range ch {
			if multiplexer.IsTimeout(e) {
				continue
			}
			s.logger.Error("unexpected error in Bidirectional", zap.Error(e), zap.Any("pair", pair))
		}
	}(errCh)
	return nil
}
