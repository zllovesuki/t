package state

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/multiplexer"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// PeerMap maintains the mapping between a Peer and their corresponding
// *multiplexer.Peer connection. Since the Peers are discovered via gossip
// and two peers in the same multiplex.Pair can attempt to handshake at the same time,
// a RingMutex with sync.Map is used to provide locking and reduce contentions
// for other connecting Peers. When a disjointed set of Peers joined, they can make progress,
// but only either the initiator or the responder of the same multiplexer.Pair can
// hold a full-duplex session.
type PeerMap struct {
	self       uint64
	peersMutex *RingMutex
	peers      sync.Map
	logger     *zap.Logger
	notify     chan *multiplexer.Peer
	num        *uint64
}

func NewPeerMap(logger *zap.Logger, self uint64) *PeerMap {
	return &PeerMap{
		peersMutex: NewRing(73),
		notify:     make(chan *multiplexer.Peer, 16),
		logger:     logger,
		num:        new(uint64),
		self:       self,
	}
}

type PeerConfig struct {
	Conn      net.Conn
	Peer      uint64
	Initiator bool
	Wait      time.Duration
}

func (s *PeerMap) NewPeer(ctx context.Context, conf PeerConfig) error {
	unlock := s.peersMutex.Lock(conf.Peer)
	defer unlock()

	if _, ok := s.peers.Load(conf.Peer); ok {
		return errors.New("already have session with this peer")
	}

	p, err := multiplexer.NewPeer(multiplexer.PeerConfig{
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

	s.logger.Info("RTT with Peer", zap.Uint64("peerID", conf.Peer), zap.Duration("rtt", d))

	select {
	case <-p.NotifyClose():
		return errors.New("peer closed after negotiation")
	case <-time.After(conf.Wait):
	}

	s.logger.Info("peer registered", zap.Bool("initiator", conf.Initiator), zap.Uint64("peerID", conf.Peer))

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

func (s *PeerMap) Notify() <-chan *multiplexer.Peer {
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
	rUnlock := s.peersMutex.RLock(peer)
	_, ok := s.peers.Load(peer)
	rUnlock()
	return ok
}

func (s *PeerMap) Get(peer uint64) *multiplexer.Peer {
	rUnlock := s.peersMutex.RLock(peer)
	p, ok := s.peers.Load(peer)
	rUnlock()
	if ok {
		return p.(*multiplexer.Peer)
	}
	return nil
}

func (s *PeerMap) Remove(peer uint64) error {
	unlock := s.peersMutex.Lock(peer)
	p, ok := s.peers.LoadAndDelete(peer)
	atomic.AddUint64(s.num, ^uint64(0))
	unlock()
	if !ok {
		return nil
	}
	return p.(*multiplexer.Peer).Bye()
}
