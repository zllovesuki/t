package state

import (
	"fmt"
	"strings"
	"sync"
)

type PeerGraph struct {
	mu    sync.RWMutex
	edges map[uint64][]uint64
}

func NewPeerGraph(self uint64) *PeerGraph {
	return &PeerGraph{
		edges: map[uint64][]uint64{
			self: {},
		},
	}
}

func (p *PeerGraph) HasPeer(peer uint64) bool {
	p.mu.RLock()
	_, has := p.edges[peer]
	p.mu.RUnlock()
	return has
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

func (p *PeerGraph) AddEdge(src, dst uint64) {
	p.mu.Lock()
	p.edges[src] = append(p.edges[src], dst)
	p.edges[dst] = append(p.edges[dst], src)
	p.mu.Unlock()
}

func (p *PeerGraph) RemovePeer(peer uint64) {
	p.mu.Lock()
	var length int
	for _, neighbor := range p.edges[peer] {
		for i, d := range p.edges[neighbor] {
			if d == peer {
				length = len(p.edges[neighbor])
				p.edges[neighbor][i] = p.edges[neighbor][length-1]
				p.edges[neighbor] = p.edges[neighbor][:length-1]
			}
		}
	}
	delete(p.edges, peer)
	p.mu.Unlock()
}

func (p *PeerGraph) Merge(c ConnectedClients) {
}

func (p *PeerGraph) String() string {
	p.mu.RLock()
	sb := strings.Builder{}
	for peer, neighbors := range p.edges {
		sb.WriteString(fmt.Sprint(peer))
		sb.WriteString(" -> ")
		for _, neighbor := range neighbors {
			sb.WriteString(fmt.Sprint(neighbor))
			sb.WriteString(" ")
		}
		sb.WriteString("\n")
	}
	p.mu.RUnlock()
	return sb.String()
}
