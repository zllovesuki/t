package server

import (
	"context"
	"encoding/base64"
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
	updatesCh      chan *state.ConnectedClients
}

func New(conf Config) (*Server, error) {

	if err := conf.validate(); err != nil {
		return nil, err
	}

	rand.Seed(time.Now().UnixNano())
	peerPort := conf.Multiplexer.Peer
	if peerPort == 0 {
		addr, ok := conf.PeerListener.Addr().(*net.TCPAddr)
		if !ok {
			return nil, errors.New("peerListener is not TLS/TCp")
		}
		peerPort = addr.Port
	}

	self := rand.Uint64()

	pMap := state.NewPeerMap(conf.Logger, self)
	cMap := state.NewPeerMap(conf.Logger, self)
	pg := state.NewPeerGraph(self)

	s := &Server{
		meta: Meta{
			PeerID:      self,
			Multiplexer: fmt.Sprintf("%s:%d", conf.Multiplexer.Addr, peerPort),
		},
		parentCtx:      conf.Context,
		config:         conf,
		peers:          pMap,
		clients:        cMap,
		peerGraph:      pg,
		id:             self,
		peerListner:    conf.PeerListener,
		clientListener: conf.ClientListener,
		logger:         conf.Logger,
		updatesCh:      make(chan *state.ConnectedClients, 16),
		broadcasts: &memberlist.TransmitLimitedQueue{
			NumNodes: func() int {
				// include ourself
				return pMap.Len() + 1
			},
			RetransmitMult: 1,
		},
	}

	keys := [][]byte{}
	for _, d := range conf.Gossip.Keyring {
		b, err := base64.StdEncoding.DecodeString(d)
		if err != nil {
			return nil, errors.Wrap(err, "decoding base64 keyring")
		}
		keys = append(keys, b)
	}

	keyring, err := memberlist.NewKeyring(keys, keys[0])
	if err != nil {
		return nil, errors.Wrap(err, "creating gossip keyring")
	}

	c := memberlist.DefaultWANConfig()
	c.AdvertiseAddr = conf.Multiplexer.Addr
	c.AdvertisePort = conf.Gossip.Port
	c.BindAddr = conf.Multiplexer.Addr
	c.BindPort = conf.Gossip.Port
	c.Keyring = keyring
	c.EnableCompression = true
	c.GossipVerifyIncoming = true
	c.GossipVerifyOutgoing = true
	c.PushPullInterval = time.Second * 30 // faster convergence
	c.Events = s
	c.Delegate = s
	c.Name = fmt.Sprintf("%s:%d/%d", conf.Multiplexer.Addr, peerPort, self)
	c.LogOutput = io.Discard

	s.gossipCfg = c

	return s, nil
}

func (s *Server) ListenForPeers() {
	go func() {
		for {
			<-time.After(time.Second * 15)
			fmt.Printf("%+v\n", s.peerGraph)
		}
	}()
	go func() {
		for {
			conn, err := s.peerListner.Accept()
			if err != nil {
				s.logger.Error("accepting TCP connection from peers", zap.Error(err))
				return
			}
			go s.peerHandshake(conn)
		}
	}()
	go s.handlePeerEvents()
	go s.handleMerge()
}

func (s *Server) ListenForClients() {
	go func() {
		for {
			conn, err := s.clientListener.Accept()
			if err != nil {
				s.logger.Error("accepting TCP connection from clients", zap.Error(err))
				return
			}
			go s.clientHandshake(conn)
		}
	}()
	go s.handleClientEvents()
}

func (s *Server) PeerID() uint64 {
	return s.id
}

func (s *Server) Meta() []byte {
	m := s.meta
	return m.Pack()
}

func (s *Server) clientHandshake(conn net.Conn) {
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

	logger.Debug("incoming handshake with client", zap.Any("peer", pair))

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

	err = s.clients.NewPeer(s.parentCtx, state.PeerConfig{
		Conn:      conn,
		Peer:      pair.Source,
		Initiator: false,
		Wait:      time.Second,
	})
	if err != nil {
		err = errors.Wrap(err, "setting up client")
	}
}

func (s *Server) peerHandshake(conn net.Conn) {
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

	logger.Debug("incoming handshake with peer", zap.Any("peer", pair))

	if (pair.Source == 0 || pair.Destination == 0) ||
		(pair.Source == pair.Destination) ||
		(pair.Destination != s.PeerID()) ||
		pair.Source == s.PeerID() {
		err = errors.Errorf("invalid peer handshake %+v", pair)
		return
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	err = s.peers.NewPeer(s.parentCtx, state.PeerConfig{
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
	// does the destination exist in our peer graph?
	if !s.peerGraph.HasPeer(pair.Destination) {
		return nil
	}
	// is the client connected locally?
	if p := s.clients.Get(pair.Destination); p != nil {
		return p
	}
	// TODO(zllovesuki): this allows for future rtt lookup for multi-peer client
	// get a random peer from the graph, if any
	peers := s.peerGraph.GetEdges(pair.Destination)
	if len(peers) == 0 {
		return nil
	}
	return s.peers.Get(peers[rand.Intn(len(peers))])
}

func (s *Server) Forward(ctx context.Context, conn net.Conn, pair multiplexer.Pair) (<-chan error, error) {
	p := s.findPath(pair)
	if p == nil {
		return nil, errors.Wrapf(ErrDestinationNotFound, "peer %d not found in peer graph", pair.Destination)
	}
	return p.Bidirectional(ctx, conn, pair)
}
