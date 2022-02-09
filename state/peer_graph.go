package state

import (
	"sync"
)

type PeerGraph struct {
	peerFilter sync.Map
}

func NewPeerGraph(self uint64) *PeerGraph {
	return &PeerGraph{}
}

func (p *PeerGraph) Who(client uint64) (peer uint64) {
	p.peerFilter.Range(func(key, value interface{}) bool {
		c := value.(ConnectedClients)
		if c.Has(client) {
			peer = key.(uint64)
			return false
		}
		return true
	})
	return
}

func (p *PeerGraph) Remove(peer uint64) {
	p.peerFilter.Delete(peer)
}

func (p *PeerGraph) Replace(c ConnectedClients) (modified bool) {
	old, loaded := p.peerFilter.LoadOrStore(c.Peer, c)
	if !loaded {
		modified = true
	} else {
		modified = old.(ConnectedClients).CRC64 != c.CRC64
		if modified {
			p.peerFilter.Store(c.Peer, c)
		}
	}
	return
}
