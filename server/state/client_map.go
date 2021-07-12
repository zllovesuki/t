package state

import (
	"fmt"
	"sync"
)

type ClientMap struct {
	mu         sync.Mutex
	self       uint64
	clientPeer map[uint64]uint64
	peerClient map[uint64]uint64
}

func NewClientMap(self uint64) *ClientMap {
	return &ClientMap{
		self:       self,
		clientPeer: make(map[uint64]uint64),
		peerClient: make(map[uint64]uint64),
	}
}

func (c *ClientMap) Has(client uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, ok := c.clientPeer[client]

	return ok
}

func (c *ClientMap) GetPeer(client uint64) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.clientPeer[client]
}

func (c *ClientMap) Put(client, peer uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.clientPeer[client] = peer
	c.peerClient[peer] = client
}

func (c *ClientMap) RemoveClient(client uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	p := c.clientPeer[client]
	delete(c.clientPeer, client)
	delete(c.peerClient, p)
}

func (c *ClientMap) RemovePeer(peer uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	client := c.peerClient[peer]
	delete(c.clientPeer, client)
	delete(c.peerClient, peer)
}

func (c *ClientMap) Print() {
	c.mu.Lock()
	defer c.mu.Unlock()

	fmt.Printf("[cm] remotely connected clients: %+v\n", c.clientPeer)
}
