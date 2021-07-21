package state

import (
	"fmt"
	"strings"
	"sync"
)

// PeerGraph is a simple implementation of undirected graph with a maximum degrees
// of two. It stores what clients our peers have, and what peers we are connected to.
type PeerGraph struct {
	self  uint64
	mu    sync.RWMutex
	edges map[uint64][]uint64
}

func NewPeerGraph(self uint64) *PeerGraph {
	return &PeerGraph{
		self: self,
		edges: map[uint64][]uint64{
			self: {},
		},
	}
}

func (p *PeerGraph) ring(peer uint64) uint64 {
	ring := peer
	for _, d := range p.edges[peer] {
		if d == p.self {
			continue
		}
		ring ^= d
	}
	return ring
}

func (p *PeerGraph) HasPeer(peer uint64) (has bool) {
	p.mu.RLock()
	_, has = p.edges[peer]
	p.mu.RUnlock()
	return
}

func (p *PeerGraph) GetEdges(peer uint64) []uint64 {
	p.mu.RLock()
	peers := p.edges[peer]
	if len(peers) == 0 {
		p.mu.RUnlock()
		return nil
	}
	edges := make([]uint64, len(peers))
	copy(edges, peers)
	p.mu.RUnlock()
	return edges
}

func (p *PeerGraph) AddEdges(src, dst uint64) {
	p.mu.Lock()
	p.addEdges(src, dst)
	p.mu.Unlock()
}

func (p *PeerGraph) addEdges(src, dst uint64) {
	p.edges[src] = append(p.edges[src], dst)
	p.edges[dst] = append(p.edges[dst], src)
}

func (p *PeerGraph) RemovePeer(peer uint64) {
	p.mu.Lock()
	p.removePeer(peer)
	p.mu.Unlock()
}

func (p *PeerGraph) removePeer(peer uint64) {
	var length int
	for _, neighbor := range p.edges[peer] {
		for i, d := range p.edges[neighbor] {
			if d == peer {
				length = len(p.edges[neighbor])
				p.edges[neighbor][i] = p.edges[neighbor][length-1]
				p.edges[neighbor] = p.edges[neighbor][:length-1]
			}
		}
		if len(p.edges[neighbor]) == 0 && neighbor != p.self {
			delete(p.edges, neighbor)
		}
	}
	delete(p.edges, peer)
}

func (p *PeerGraph) Replace(c *ConnectedClients) (modified bool) {
	p.mu.Lock()
	if p.ring(c.Peer) == c.Ring() {
		p.mu.Unlock()
		return
	}
	for _, client := range p.edges[c.Peer] {
		if client == p.self {
			continue
		}
		p.removePeer(client)
	}
	for _, client := range c.Clients {
		p.addEdges(c.Peer, client)
	}
	modified = true
	p.mu.Unlock()
	return
}

func (p *PeerGraph) String() string {
	p.mu.RLock()
	sb := strings.Builder{}
	for peer, neighbors := range p.edges {
		sb.WriteString(fmt.Sprint(peer))
		sb.WriteString(" -> ")
		sb.WriteString(fmt.Sprint(neighbors))
		sb.WriteString("\n")
	}
	p.mu.RUnlock()
	return sb.String()
}
