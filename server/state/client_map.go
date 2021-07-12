package state

import (
	"fmt"
	"sync"
)

// TODO(zllovesuki): Consider using a different data structure
// such as graph for this purpose
type ClientMap struct {
	mu          sync.RWMutex
	self        uint64
	clientPeer  map[uint64]uint64
	peerClients map[uint64][]uint64
}

func NewClientMap(self uint64) *ClientMap {
	return &ClientMap{
		self:        self,
		clientPeer:  make(map[uint64]uint64),
		peerClients: make(map[uint64][]uint64),
	}
}

func (c *ClientMap) Has(client uint64) bool {
	c.mu.RLock()
	_, ok := c.clientPeer[client]
	c.mu.RUnlock()

	return ok
}

func (c *ClientMap) GetPeer(client uint64) (uint64, bool) {
	c.mu.RLock()
	peer, ok := c.clientPeer[client]
	c.mu.RUnlock()
	return peer, ok
}

func (c *ClientMap) Put(client, peer uint64) {
	c.mu.Lock()
	c.clientPeer[client] = peer
	c.peerClients[peer] = append(c.peerClients[peer], client)
	c.mu.Unlock()
}

func (c *ClientMap) RemoveClient(client uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.clientPeer[client]
	if !ok {
		return
	}
	delete(c.clientPeer, client)
	target := -1
	for k, v := range c.peerClients[p] {
		if v == client {
			target = k
			break
		}
	}
	if target == -1 {
		return
	}
	if target == 0 {
		delete(c.peerClients, p)
		return
	}
	c.peerClients[p][target] = c.peerClients[p][len(c.peerClients[p])-1]
	c.peerClients[p] = c.peerClients[p][:len(c.peerClients[p])-1]
}

func (c *ClientMap) RemovePeer(peer uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	clients, ok := c.peerClients[peer]
	if !ok {
		return
	}
	for _, v := range clients {
		delete(c.clientPeer, v)
	}
	delete(c.peerClients, peer)
}

func (c *ClientMap) Print() {
	c.mu.RLock()
	fmt.Printf("[cm] remotely connected clients: %+v\n", c.clientPeer)
	c.mu.RUnlock()
}
