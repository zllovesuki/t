package state

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"go.uber.org/zap"

	"github.com/pkg/errors"
)

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
		peersMutex: NewRing(43),
		notify:     make(chan *multiplexer.Peer),
		self:       self,
		logger:     logger,
		num:        new(uint64),
	}
}

type PeerConfig struct {
	Conn      net.Conn
	Peer      uint64
	Initiator bool
	Wait      time.Duration
}

func (s *PeerMap) NewPeer(ctx context.Context, conf PeerConfig) error {

	rUnlock := s.peersMutex.RLock(conf.Peer)
	if _, ok := s.peers.Load(conf.Peer); ok {
		rUnlock()
		return errors.New("already have session with this peer")
	}

	rUnlock()
	unlock := s.peersMutex.Lock(conf.Peer)
	defer unlock()

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

func (s *PeerMap) Notify() <-chan *multiplexer.Peer {
	return s.notify
}

func (s *PeerMap) Len() uint64 {
	return atomic.LoadUint64(s.num)
}

func (s *PeerMap) Print() {
	peers := []string{}
	s.peers.Range(func(key, value interface{}) bool {
		peers = append(peers, fmt.Sprint(key.(uint64)))
		return true
	})
	fmt.Printf("[pm] Connected peers: %s\n", strings.Join(peers, ", "))
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
	unlock()
	if !ok {
		return nil
	}
	atomic.AddUint64(s.num, ^uint64(0))
	return p.(*multiplexer.Peer).Bye()
}
