package state

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/zllovesuki/t/multiplexer"

	"github.com/pkg/errors"
)

type PeerMap struct {
	mu    sync.Mutex
	self  int64
	peers map[int64]*multiplexer.Peer
}

func NewPeerMap(self int64) *PeerMap {
	return &PeerMap{
		peers: make(map[int64]*multiplexer.Peer),
		self:  self,
	}
}

func (s *PeerMap) NewPeer(ctx context.Context, conn net.Conn, peer int64, client bool) (*multiplexer.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Printf("NewPeer: %+v\n", peer)

	if _, ok := s.peers[peer]; ok {
		return nil, errors.New("already have session with this pair")
	}

	fmt.Print("creating new session\n")

	p, err := multiplexer.NewPeer(multiplexer.PeerConfig{
		Conn:      conn,
		Initiator: client,
		Peer:      peer,
	})
	if err != nil {
		return nil, err
	}
	s.peers[peer] = p

	fmt.Printf("peer registered: %+v\n", peer)

	return p, nil
}

func (s *PeerMap) Print() {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Printf("%+v\n", s.peers)
}

func (s *PeerMap) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.peers)/2 + 1
}

func (s *PeerMap) Get(peer int64) *multiplexer.Peer {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.peers[peer]; ok {
		return p
	}

	return nil
}

func (s *PeerMap) Remove(peer int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.peers[peer]
	if !ok {
		return nil
	}

	delete(s.peers, peer)

	return p.Bye()
}
