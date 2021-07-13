package state

import (
	"encoding/binary"

	"github.com/hashicorp/memberlist"
)

const (
	ClientUpdateSize = 32
)

type ClientUpdate struct {
	Peer      uint64
	Client    uint64
	Counter   uint64
	Connected bool
}

func (c *ClientUpdate) Unpack(b []byte) {
	var x ClientUpdate
	x.Peer = binary.BigEndian.Uint64(b[0:8])
	x.Client = binary.BigEndian.Uint64(b[8:16])
	x.Counter = binary.BigEndian.Uint64(b[16:24])
	if b[26] == 1 {
		x.Connected = true
	}
	*c = x
}

func (c *ClientUpdate) Pack() []byte {
	b := make([]byte, ClientUpdateSize)

	binary.BigEndian.PutUint64(b[0:8], c.Peer)
	binary.BigEndian.PutUint64(b[8:16], c.Client)
	binary.BigEndian.PutUint64(b[16:24], c.Counter)
	if c.Connected {
		b[26] = 1
	}

	return b
}

var _ memberlist.Broadcast = &ClientUpdate{}

func (c *ClientUpdate) Invalidates(b memberlist.Broadcast) bool {
	return false
}

func (c *ClientUpdate) Message() []byte {
	return c.Pack()
}

func (c *ClientUpdate) Finished() {

}
