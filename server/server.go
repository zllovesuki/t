package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net"
	"time"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/messaging"
	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"

	"github.com/hashicorp/memberlist"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Server struct {
	parentCtx      context.Context
	logger         *zap.Logger
	config         Config
	id             uint64
	meta           Meta
	peers          *state.PeerMap
	clients        *state.PeerMap
	peerGraph      *state.PeerGraph
	messaging      *messaging.Channel
	peerListner    net.Listener
	clientListener net.Listener
	gossip         *memberlist.Memberlist
	gossipCfg      *memberlist.Config
	broadcasts     *memberlist.TransmitLimitedQueue
	updatesCh      chan *state.ConnectedClients
	currentLeader  *uint64
	membershipCh   chan struct{}
	startLeader    chan struct{}
	stopLeader     chan struct{}
	certManager    *acme.CertManager
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
			return nil, errors.New("peerListener is not TLS/TCP")
		}
		peerPort = addr.Port
	}

	self := rand.Uint64()

	pMap := state.NewPeerMap(conf.Logger, self)
	cMap := state.NewPeerMap(conf.Logger, self)
	pg := state.NewPeerGraph(self)

	s := &Server{
		meta: Meta{
			ConnectIP:   conf.Network.AdvertiseAddr,
			ConnectPort: uint64(peerPort),
			PeerID:      self,
		},
		parentCtx:      conf.Context,
		config:         conf,
		id:             self,
		peers:          pMap,
		clients:        cMap,
		peerGraph:      pg,
		messaging:      messaging.New(conf.Logger, self),
		peerListner:    conf.PeerListener,
		clientListener: conf.ClientListener,
		logger:         conf.Logger,
		updatesCh:      make(chan *state.ConnectedClients, 16),
		currentLeader:  new(uint64),
		membershipCh:   make(chan struct{}, 1),
		startLeader:    make(chan struct{}, 1),
		stopLeader:     make(chan struct{}, 1),
		certManager:    conf.CertManager,
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
	if len(keys) == 0 {
		return nil, errors.New("no gossip keyring was found")
	}

	keyring, err := memberlist.NewKeyring(keys, keys[rand.Intn(len(keys))])
	if err != nil {
		return nil, errors.Wrap(err, "creating gossip keyring")
	}

	c := memberlist.DefaultWANConfig()
	c.AdvertiseAddr = conf.Network.AdvertiseAddr
	c.AdvertisePort = conf.Gossip.Port
	c.BindAddr = conf.Network.BindAddr
	c.BindPort = conf.Gossip.Port
	c.Keyring = keyring
	c.EnableCompression = true
	c.GossipVerifyIncoming = true
	c.GossipVerifyOutgoing = true
	c.PushPullInterval = time.Second * 30 // faster convergence
	c.Events = s
	c.Delegate = s
	c.Name = fmt.Sprintf("%s:%d/%d", conf.Network.AdvertiseAddr, peerPort, self)
	c.LogOutput = io.Discard

	s.gossipCfg = c

	return s, nil
}

func (s *Server) ListenForPeers() {
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
	go s.handleMembershipChange()
	go s.handlePeerEvents()
	go s.handleMerge()
	s.membershipCh <- struct{}{}
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
	return s.meta.Pack()
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
