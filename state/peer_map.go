package state

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc64"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/protocol"
	_ "github.com/zllovesuki/t/mux"

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
	peers  sync.Map
	logger *zap.Logger
	notify chan multiplexer.Peer
	num    *uint64
}

func NewPeerMap(logger *zap.Logger, self uint64) *PeerMap {
	return &PeerMap{
		peers:  sync.Map{},
		notify: make(chan multiplexer.Peer, 16),
		logger: logger,
		num:    new(uint64),
		self:   self,
	}
}

func (s *PeerMap) NewPeer(ctx context.Context, proto protocol.Protocol, conf multiplexer.Config) error {
	if _, ok := s.peers.Load(conf.Peer); ok {
		return ErrSessionAlreadyEstablished
	}

	c, err := multiplexer.New(proto)
	if err != nil {
		return err
	}

	p, err := c(conf)
	if err != nil {
		return err
	}

	select {
	case <-p.NotifyClose():
		return errors.New("peer closed after negotiation")
	case <-time.After(conf.Wait):
	}

	s.logger.Debug("Peer negotiation result", zap.Uint64("peerID", conf.Peer), zap.String("protocol", proto.String()))

	s.peers.Store(conf.Peer, p)
	atomic.AddUint64(s.num, 1)
	s.notify <- p

	return nil
}

func (s *PeerMap) CRC64() uint64 {
	peers := s.Snapshot()
	sort.SliceStable(peers, func(i, j int) bool {
		return peers[i] < peers[j]
	})
	b := make([]byte, 8)
	crc := crc64.New(crcTable)
	for _, peer := range peers {
		binary.BigEndian.PutUint64(b, peer)
		crc.Write(b)
	}
	return crc.Sum64()
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
	pp, ok := s.peers.LoadAndDelete(peer)
	if !ok {
		return nil
	}
	atomic.AddUint64(s.num, ^uint64(0))
	p := pp.(multiplexer.Peer)
	return p.Bye()
}
