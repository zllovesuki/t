package state

import (
	"fmt"
	"sync"
)

type ClientMap struct {
	mu         sync.Mutex
	self       int64
	clientPeer map[int64]int64
	peerClient map[int64]int64
}

func NewClientMap(self int64) *ClientMap {
	return &ClientMap{
		self:       self,
		clientPeer: make(map[int64]int64),
		peerClient: make(map[int64]int64),
	}
}

func (c *ClientMap) GetPeer(client int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.clientPeer[client]
}

func (c *ClientMap) Put(client, peer int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.clientPeer[client] = peer
	c.peerClient[peer] = client
}

func (c *ClientMap) RemoveClient(client int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	p := c.clientPeer[client]
	delete(c.clientPeer, client)
	delete(c.peerClient, p)
}

func (c *ClientMap) RemovePeer(peer int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	client := c.peerClient[peer]
	delete(c.clientPeer, client)
	delete(c.peerClient, peer)
}

func (c *ClientMap) Print() {
	c.mu.Lock()
	defer c.mu.Unlock()

	fmt.Printf("%+v %+v\n", c.clientPeer, c.peerClient)
}
