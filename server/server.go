package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/messaging"
	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/profiler"
	"github.com/zllovesuki/t/state"

	"github.com/hashicorp/memberlist"
	"github.com/lucas-clemente/quic-go"
	"go.uber.org/zap"
)

type Server struct {
	id                 uint64
	_                  [56]byte // alignment padding
	currentLeader      uint64
	_                  [56]byte // alignment padding
	parentCtx          context.Context
	logger             *zap.Logger
	config             Config
	meta               Meta
	metaBytes          atomic.Value
	peers              *state.PeerMap
	clients            *state.PeerMap
	peerGraph          *state.PeerGraph
	messaging          *messaging.Channel
	peerTLSListener    net.Listener
	peerQUICListener   quic.Listener
	clientTLSListener  net.Listener
	clientQuicListener quic.Listener
	gossip             *memberlist.Memberlist
	gossipCfg          *memberlist.Config
	broadcasts         *memberlist.TransmitLimitedQueue
	updatesCh          chan state.ConnectedClients
	cached             *cachedValue
	clientEvtCh        chan struct{}
	membershipCh       chan struct{}
	startLeader        chan struct{}
	stopLeader         chan struct{}
	certManager        *acme.CertManager
}

func New(conf Config) (*Server, error) {
	if err := conf.validate(); err != nil {
		return nil, err
	}

	rand.Seed(time.Now().UnixNano())
	peerPort := conf.Multiplexer.Peer

	self := rand.Uint64()

	pMap := state.NewPeerMap(conf.Logger, self)
	cMap := state.NewPeerMap(conf.Logger, self)
	pg := state.NewPeerGraph(self)

	s := &Server{
		meta: Meta{
			ConnectIP:   conf.Network.AdvertiseAddr,
			ConnectPort: uint64(peerPort),
			PeerID:      self,
			Protocol:    conf.Multiplexer.Protocol,
			RespondOnly: conf.Multiplexer.RespondOnly,
		},
		parentCtx:          conf.Context,
		config:             conf,
		id:                 self,
		peers:              pMap,
		clients:            cMap,
		peerGraph:          pg,
		messaging:          messaging.New(conf.Logger, self),
		peerTLSListener:    conf.PeerTLSListener,
		peerQUICListener:   conf.PeerQUICListener,
		clientTLSListener:  conf.ClientTLSListener,
		clientQuicListener: conf.ClientQUICListener,
		logger:             conf.Logger,
		cached:             &cachedValue{},
		clientEvtCh:        make(chan struct{}, 1),
		updatesCh:          make(chan state.ConnectedClients, 16),
		membershipCh:       make(chan struct{}, 1),
		startLeader:        make(chan struct{}, 1),
		stopLeader:         make(chan struct{}, 1),
		certManager:        conf.CertManager,
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
			return nil, fmt.Errorf("decoding base64 keyring: %w", err)
		}
		keys = append(keys, b)
	}
	if len(keys) == 0 {
		return nil, errors.New("no gossip keyring was found")
	}

	keyring, err := memberlist.NewKeyring(keys, keys[rand.Intn(len(keys))])
	if err != nil {
		return nil, fmt.Errorf("creating gossip keyring: %w", err)
	}

	c := memberlist.DefaultWANConfig()
	c.AdvertiseAddr = conf.Network.AdvertiseAddr
	c.AdvertisePort = conf.Gossip.Port
	c.BindAddr = conf.Network.BindAddr
	c.BindPort = conf.Gossip.Port
	c.Keyring = keyring
	c.EnableCompression = false
	c.GossipVerifyIncoming = true
	c.GossipVerifyOutgoing = true
	c.PushPullInterval = time.Second * 30 // faster convergence
	c.Events = s
	c.Delegate = s
	c.Ping = s
	c.Name = fmt.Sprintf("%s:%d/%d", conf.Network.AdvertiseAddr, peerPort, self)
	c.LogOutput = io.Discard

	s.gossipCfg = c

	// since meta is read-only, storing the bytes and read it atomically
	// from gossip should help with allocation
	b, err := s.meta.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal meta: %w", err)
	}
	s.metaBytes.Store(b)

	go s.backgroundDispatcher()

	return s, nil
}

func (s *Server) ListenForPeers() {
	go func() {
		if !s.config.Debug {
			return
		}
		for {
			time.Sleep(time.Second * 15)
			s.messaging.Announce(messaging.MessageTest, []byte{1, 2, 3, 4})
		}
	}()
	go func() {
		for {
			conn, err := s.peerTLSListener.Accept()
			if err != nil {
				s.logger.Error("accepting TCP connection from peers", zap.Error(err))
				return
			}
			go s.peerTLSHandshake(conn)
		}
	}()
	go func() {
		for {
			sess, err := s.peerQUICListener.Accept(s.parentCtx)
			if err != nil {
				s.logger.Error("accepting quic connection from peers", zap.Error(err))
				return
			}
			go s.peerQUICHandshake(sess)
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
			conn, err := s.clientTLSListener.Accept()
			if err != nil {
				s.logger.Error("accepting tls connection from clients", zap.Error(err))
				return
			}
			go s.clientTLSHandshake(conn)
		}
	}()
	go func() {
		for {
			sess, err := s.clientQuicListener.Accept(s.parentCtx)
			if err != nil {
				s.logger.Error("accepting quic connections from clients", zap.Error(err))
				return
			}
			go s.clientQUICHandshake(sess)
		}
	}()
	go s.handleClientEvents()
}

func (s *Server) PeerID() uint64 {
	return s.id
}

func (s *Server) findPath(link multiplexer.Link) multiplexer.Peer {
	// is the client connected locally?
	if p := s.clients.Get(link.Destination); p != nil {
		return p
	}
	// does the destination exist in our peer graph?
	p := s.peerGraph.Who(link.Destination)
	return s.peers.Get(p) // nil if no one has it
}

func (s *Server) Forward(ctx context.Context, conn net.Conn, link multiplexer.Link) (<-chan error, error) {
	p := s.findPath(link)
	if p == nil {
		return nil, fmt.Errorf("peer %d not found in peer graph: %w", link.Destination, multiplexer.ErrDestinationNotFound)
	}
	s.logger.Debug("opening new forwarding link", zap.Object("link", link))
	profiler.ConnectionStats.WithLabelValues("new", "bidirectional").Inc()
	return p.Bidirectional(ctx, conn, link)
}

func (s *Server) Direct(ctx context.Context, link multiplexer.Link) (net.Conn, error) {
	p := s.findPath(link)
	if p == nil {
		return nil, fmt.Errorf("peer %d not found in peer graph: %w", link.Destination, multiplexer.ErrDestinationNotFound)
	}
	s.logger.Debug("opening new direct connection link", zap.Object("link", link))
	profiler.ConnectionStats.WithLabelValues("new", "direct").Inc()
	return p.Direct(ctx, link)
}
