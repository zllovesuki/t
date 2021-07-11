package state

import (
	"encoding/binary"
)

type ClientUpdate struct {
	Peer      int64
	Client    int64
	Connected bool
}

func (c *ClientUpdate) Unpack(b []byte) {
	var x ClientUpdate
	x.Peer = int64(binary.BigEndian.Uint64(b[0:8]))
	x.Client = int64(binary.BigEndian.Uint64(b[8:16]))
	if b[17] == 1 {
		x.Connected = true
	}
	*c = x
}

func (c *ClientUpdate) Pack() []byte {
	b := make([]byte, 32)

	binary.BigEndian.PutUint64(b[0:8], uint64(c.Peer))
	binary.BigEndian.PutUint64(b[8:16], uint64(c.Client))
	if c.Connected {
		b[17] = 1
	}

	return b
}
