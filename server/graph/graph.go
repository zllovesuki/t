package graph

import (
	"fmt"
	"strings"
	"sync"
)

type PeerGraph struct {
	mu    sync.RWMutex
	Peers []uint64
	Edges map[uint64][]uint64
}

func NewPeerGraph() *PeerGraph {
	return &PeerGraph{
		Peers: make([]uint64, 0),
		Edges: make(map[uint64][]uint64),
	}
}

func (p *PeerGraph) HasPeer(peer uint64) bool {
	p.mu.RLock()
	has := false
	for _, p := range p.Peers {
		if p == peer {
			has = true
			break
		}
	}
	p.mu.RUnlock()
	return has
}

func (p *PeerGraph) GetEdges(peer uint64) []uint64 {
	p.mu.RLock()
	if _, ok := p.Edges[peer]; !ok {
		p.mu.RUnlock()
		return nil
	}
	edges := make([]uint64, len(p.Edges[peer]))
	copy(edges, p.Edges[peer])
	p.mu.RUnlock()
	return edges
}

func (p *PeerGraph) AddPeer(peer uint64) {
	p.mu.Lock()
	p.Peers = append(p.Peers, peer)
	p.mu.Unlock()
}

func (p *PeerGraph) AddEdge(src, dst uint64) {
	p.mu.Lock()
	if p.Edges[src] == nil {
		p.Edges[src] = make([]uint64, 0)
	}
	if p.Edges[dst] == nil {
		p.Edges[dst] = make([]uint64, 0)
	}
	p.Edges[src] = append(p.Edges[src], dst)
	p.Edges[dst] = append(p.Edges[dst], src)
	p.mu.Unlock()
}

// TODO(zllovesuki): there has to be a more efficient way
func (p *PeerGraph) RemovePeer(peer uint64) {
	p.mu.Lock()
	var length int
	for _, neighbor := range p.Edges[peer] {
		for i, d := range p.Edges[neighbor] {
			length = len(p.Edges[neighbor])
			if d == peer {
				p.Edges[neighbor][i] = p.Edges[neighbor][length-1]
				p.Edges[neighbor] = p.Edges[neighbor][:length-1]
			}
		}
	}
	for i, pe := range p.Peers {
		if pe == peer {
			p.Peers[i] = p.Peers[len(p.Peers)-1]
			p.Peers = p.Peers[:len(p.Peers)-1]
		}
	}
	delete(p.Edges, peer)
	p.mu.Unlock()
}

func (p *PeerGraph) String() string {
	p.mu.RLock()
	sb := strings.Builder{}
	for _, peer := range p.Peers {
		sb.WriteString(fmt.Sprint(peer))
		sb.WriteString(" -> ")
		neighbors := p.Edges[peer]
		for _, neighbor := range neighbors {
			sb.WriteString(fmt.Sprint(neighbor))
			sb.WriteString(" ")
		}
		sb.WriteString("\n")
	}
	p.mu.RUnlock()
	return sb.String()
}
