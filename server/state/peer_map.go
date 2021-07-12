package state

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/zllovesuki/t/multiplexer"
	"go.uber.org/zap"

	"github.com/pkg/errors"
)

type PeerMap struct {
	mu     sync.Mutex
	self   uint64
	peers  map[uint64]*multiplexer.Peer
	logger *zap.Logger
}

func NewPeerMap(logger *zap.Logger, self uint64) *PeerMap {
	return &PeerMap{
		peers:  make(map[uint64]*multiplexer.Peer),
		self:   self,
		logger: logger,
	}
}

func (s *PeerMap) NewPeer(ctx context.Context, conn net.Conn, peer uint64, client bool) (*multiplexer.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// fmt.Printf("NewPeer: %+v\n", peer)

	if _, ok := s.peers[peer]; ok {
		return nil, errors.New("already have session with this pair")
	}

	// fmt.Print("creating new session\n")

	p, err := multiplexer.NewPeer(multiplexer.PeerConfig{
		Conn:      conn,
		Initiator: client,
		Peer:      peer,
	})
	if err != nil {
		return nil, err
	}
	s.peers[peer] = p

	s.logger.Info("peer registered", zap.Uint64("peerID", peer))

	return p, nil
}

func (s *PeerMap) Print() {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Printf("[pm] connected peers: %+v\n", s.peers)
}

func (s *PeerMap) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.peers)/2 + 1
}

func (s *PeerMap) Has(peer uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.peers[peer]

	return ok
}

func (s *PeerMap) Get(peer uint64) *multiplexer.Peer {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.peers[peer]; ok {
		return p
	}

	return nil
}

func (s *PeerMap) Remove(peer uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.peers[peer]
	if !ok {
		return nil
	}

	delete(s.peers, peer)

	return p.Bye()
}
