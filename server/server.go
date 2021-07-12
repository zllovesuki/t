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
	parentCtx     context.Context
	id            uint64
	meta          Meta
	peers         *state.PeerMap
	clients       *state.PeerMap
	remoteClients *state.ClientMap
	notifier      *state.Notifier
	listener      net.Listener
	logger        *zap.Logger
	gossip        *memberlist.Memberlist
	gossipCfg     *memberlist.Config
	config        Config
}

type Config struct {
	Logger   *zap.Logger
	Listener net.Listener
	Gossip   struct {
		IP      string
		Port    int
		Members []string
	}
}

func New(conf Config) (*Server, error) {
	rand.Seed(time.Now().UnixNano())

	self := rand.Uint64()
	addr := conf.Listener.Addr().(*net.TCPAddr)

	pMap := state.NewPeerMap(conf.Logger, self)
	cMap := state.NewPeerMap(conf.Logger, self)
	rcMap := state.NewClientMap(self)

	s := &Server{
		meta: Meta{
			PeerID:      self,
			Multiplexer: fmt.Sprintf("%s:%d", addr.IP, addr.Port),
		},
		peers:         pMap,
		clients:       cMap,
		remoteClients: rcMap,
		notifier:      state.NewNotifer(),
		id:            self,
		listener:      conf.Listener,
		logger:        conf.Logger,
		config:        conf,
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
			<-time.After(time.Second * 10)
			fmt.Printf("%d\n", s.PeerID())
			s.peers.Print()
			s.clients.Print()
			s.remoteClients.Print()
		}
	}()
	for {
		conn, err := s.listener.Accept()
		// fmt.Printf("new connection\n")
		if err != nil {
			s.logger.Error("accepting TCP connection", zap.Error(err))
			return
		}
		go s.transportHandshake(ctx, conn)
	}
}

func (s *Server) PeerID() uint64 {
	return s.id
}

func (s *Server) Meta() []byte {
	return s.meta.Pack()
}

func (s *Server) transportHandshake(ctx context.Context, conn net.Conn) {
	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		logger.Error("error during handshake", zap.Error(err))
		conn.Close()
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

	// TODO(zllovesuki): possible race condition
	if s.peers.Has(pair.Source) || s.clients.Has(pair.Source) || s.remoteClients.Has(pair.Source) {
		err = errors.Errorf("duplicate peerID %d\n", pair.Source)
		return
	}
	if pair.Source == 0 {
		err = errors.Errorf("invalid source peerID %d", pair.Source)
		return
	}
	if pair.Source == s.PeerID() {
		err = errors.Errorf("loop: connecting to ourself %+v", pair)
		return
	}

	var p *multiplexer.Peer

	if pair.Destination == 0 {
		logger.Info("handshake from client")

		pair.Destination = s.PeerID()
		w := pair.Pack()
		_, err = conn.Write(w)
		if err != nil {
			err = errors.Wrap(err, "replying handshake")
			return
		}

		p, err = s.clients.NewPeer(ctx, conn, pair.Source, false)
		if err != nil {
			err = errors.Wrap(err, "setting up client")
			return
		}

		go s.clientListener(ctx, p)
	} else {
		logger.Info("handshake from peer")

		var m Meta
		var found bool
		for _, node := range s.gossip.Members() {
			err := m.Unpack(node.Meta)
			if err != nil {
				return
			}
			if pair.Source == m.PeerID {
				found = true
				break
			}
		}

		if !found {
			err = errors.Errorf("peer %d is not gossiping with us", pair.Source)
			return
		}

		p, err = s.peers.NewPeer(ctx, conn, pair.Source, false)
		if err != nil {
			err = errors.Wrap(err, "setting up peer")
			return
		}
		go s.peerListeners(ctx, p)
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})
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
