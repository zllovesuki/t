package state

import "encoding/binary"

// ConnectedClients is used when memberlist does TCP push/pull
// state synchronization. It notifies our peers about the list of
// clients that we are connected to.
type ConnectedClients struct {
	Peer    uint64
	Clients []uint64
}

func (c *ConnectedClients) Pack() []byte {
	len := uint64(len(c.Clients))
	// first 8: length of Clients
	// next 8: Peer ID
	// the rest: connected clients
	b := make([]byte, 8+8+8*len)
	binary.BigEndian.PutUint64(b[0:8], len)
	binary.BigEndian.PutUint64(b[8:16], c.Peer)
	for i, c := range c.Clients {
		binary.BigEndian.PutUint64(b[(2+i)*8:(3+i)*8], c)
	}
	return b
}

func (c *ConnectedClients) Unpack(b []byte) {
	len := binary.BigEndian.Uint64(b[0:8])
	c.Peer = binary.BigEndian.Uint64(b[8:16])
	if c.Clients == nil {
		c.Clients = make([]uint64, len)
	}
	for i := uint64(0); i < len; i++ {
		c.Clients[i] = binary.BigEndian.Uint64(b[(2+i)*8 : (3+i)*8])
	}
}

func (c *ConnectedClients) Ring() uint64 {
	ring := c.Peer
	for _, p := range c.Clients {
		ring ^= p
	}
	return ring
}
