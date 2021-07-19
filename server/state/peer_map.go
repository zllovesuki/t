package state

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/peer"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

var (
	ErrSessionAlreadyEstablished = fmt.Errorf("already have a session with this peer")
)

// PeerMap maintains the mapping between a Peer and their corresponding
// *multiplexer.Peer connection. Although Peers are discovered via gossip,
// who gets to initiate the connection is decided by the ordinality of their
// PeerIDs. If they happened to have the same PeerID, they will panic to restart.
type PeerMap struct {
	self   uint64
	peers  *sync.Map
	logger *zap.Logger
	notify chan multiplexer.Peer
	num    *uint64
}

func NewPeerMap(logger *zap.Logger, self uint64) *PeerMap {
	return &PeerMap{
		peers:  &sync.Map{},
		notify: make(chan multiplexer.Peer, 16),
		logger: logger,
		num:    new(uint64),
		self:   self,
	}
}

type PeerConfig struct {
	Conn      net.Conn
	Peer      uint64
	Initiator bool
	Wait      time.Duration
}

func (s *PeerMap) NewPeer(ctx context.Context, conf PeerConfig) error {
	if _, ok := s.peers.Load(conf.Peer); ok {
		return ErrSessionAlreadyEstablished
	}

	p, err := peer.NewYamuxPeer(peer.YamuxConfig{
		Logger:    s.logger,
		Conn:      conf.Conn,
		Initiator: conf.Initiator,
		Peer:      conf.Peer,
	})
	if err != nil {
		return err
	}

	d, err := p.Ping()
	if err != nil {
		return errors.Wrap(err, "cannot ping peer")
	}

	s.logger.Debug("RTT with Peer", zap.Uint64("peerID", conf.Peer), zap.Duration("rtt", d))

	select {
	case <-p.NotifyClose():
		return errors.New("peer closed after negotiation")
	case <-time.After(conf.Wait):
	}

	atomic.AddUint64(s.num, 1)
	s.peers.Store(conf.Peer, p)
	s.notify <- p

	return nil
}

func (s *PeerMap) Ring() uint64 {
	ring := s.self
	peers := s.Snapshot()
	for _, peer := range peers {
		ring ^= peer
	}
	return ring
}

func (s *PeerMap) Notify() <-chan multiplexer.Peer {
	return s.notify
}

func (s *PeerMap) Len() int {
	return int(atomic.LoadUint64(s.num))
}

func (s *PeerMap) Print() {
	peers := s.Snapshot()
	fmt.Printf("[pm] Connected peers: %s\n", fmt.Sprint(peers))
}

func (s *PeerMap) Snapshot() []uint64 {
	peers := []uint64{}
	s.peers.Range(func(key, value interface{}) bool {
		if value == nil {
			return true
		}
		peers = append(peers, key.(uint64))
		return true
	})
	return peers
}

func (s *PeerMap) Has(peer uint64) bool {
	_, ok := s.peers.Load(peer)
	return ok
}

func (s *PeerMap) Get(peer uint64) multiplexer.Peer {
	p, ok := s.peers.Load(peer)
	if ok {
		return p.(multiplexer.Peer)
	}
	return nil
}

func (s *PeerMap) Remove(peer uint64) error {
	atomic.AddUint64(s.num, ^uint64(0))
	pp, ok := s.peers.LoadAndDelete(peer)
	if !ok {
		return nil
	}
	p := pp.(multiplexer.Peer)
	return p.Bye()
}
