package state

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"go.uber.org/zap"

	"github.com/pkg/errors"
)

type PeerMap struct {
	self       uint64
	peersMutex sync.Map
	peers      sync.Map
	logger     *zap.Logger
	notify     chan *multiplexer.Peer
}

func NewPeerMap(logger *zap.Logger, self uint64) *PeerMap {
	return &PeerMap{
		notify: make(chan *multiplexer.Peer),
		self:   self,
		logger: logger,
	}
}

type PeerConfig struct {
	Conn      net.Conn
	Peer      uint64
	Initiator bool
	Wait      time.Duration
}

func (s *PeerMap) locker(peer uint64) (locker func(), unlocker func()) {
	mu, _ := s.peersMutex.LoadOrStore(peer, &sync.Mutex{})
	lock := mu.(*sync.Mutex)
	return lock.Lock, lock.Unlock
}

func (s *PeerMap) NewPeer(ctx context.Context, conf PeerConfig) error {
	lock, unlock := s.locker(conf.Peer)
	lock()
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

	s.peers.Store(conf.Peer, p)
	s.notify <- p

	return nil
}

func (s *PeerMap) Notify() <-chan *multiplexer.Peer {
	return s.notify
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
	lock, unlock := s.locker(peer)
	lock()
	defer unlock()

	_, ok := s.peers.Load(peer)

	return ok
}

func (s *PeerMap) Get(peer uint64) *multiplexer.Peer {
	lock, unlock := s.locker(peer)
	lock()
	defer unlock()

	if p, ok := s.peers.Load(peer); ok {
		return p.(*multiplexer.Peer)
	}

	return nil
}

func (s *PeerMap) Remove(peer uint64) error {
	lock, unlock := s.locker(peer)
	lock()
	defer unlock()

	p, ok := s.peers.Load(peer)
	if !ok {
		return nil
	}

	s.peers.Delete(peer)

	return p.(*multiplexer.Peer).Bye()
}
