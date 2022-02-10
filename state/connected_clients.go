package state

import (
	"encoding/binary"
	"fmt"
	"hash/crc64"

	"github.com/FastFilter/xorfilter"
)

var (
	crcTable = crc64.MakeTable(crc64.ECMA)
)

// ConnectedClients is used when memberlist does TCP push/pull
// state synchronization. It is used to notify the querying peer
// with a binary fuse filter.
type ConnectedClients struct {
	Peer  uint64
	CRC64 uint64
	f     *xorfilter.BinaryFuse8
}

func NewConnectedClients(peer uint64, clients []uint64) (*ConnectedClients, error) {
	b := make([]byte, 8)
	crc := crc64.New(crcTable)
	for _, c := range clients {
		binary.BigEndian.PutUint64(b, c)
		crc.Write(b)
	}
	filter, err := xorfilter.PopulateBinaryFuse8(clients)
	if err != nil {
		return nil, err
	}
	return &ConnectedClients{
		Peer:  peer,
		CRC64: crc.Sum64(),
		f:     filter,
	}, nil
}

func (c *ConnectedClients) MarshalBinary() ([]byte, error) {
	fLen := len(c.f.Fingerprints)
	b := make([]byte, 48+fLen)
	binary.BigEndian.PutUint64(b[0:8], c.Peer)
	binary.BigEndian.PutUint64(b[8:16], c.CRC64)
	binary.BigEndian.PutUint64(b[16:24], c.f.Seed)
	binary.BigEndian.PutUint32(b[24:28], c.f.SegmentLength)
	binary.BigEndian.PutUint32(b[28:32], c.f.SegmentLengthMask)
	binary.BigEndian.PutUint32(b[32:36], c.f.SegmentCount)
	binary.BigEndian.PutUint32(b[36:40], c.f.SegmentCountLength)
	binary.BigEndian.PutUint64(b[40:48], uint64(fLen))
	copy(b[48:], c.f.Fingerprints)
	return b, nil
}

func (c *ConnectedClients) UnmarshalBinary(b []byte) error {
	if len(b) < 48 {
		return fmt.Errorf("invalid buffer length: %d", len(b))
	}
	c.Peer = binary.BigEndian.Uint64(b[0:8])
	c.CRC64 = binary.BigEndian.Uint64(b[8:16])
	c.f = &xorfilter.BinaryFuse8{}
	c.f.Seed = binary.BigEndian.Uint64(b[16:24])
	c.f.SegmentLength = binary.BigEndian.Uint32(b[24:28])
	c.f.SegmentLengthMask = binary.BigEndian.Uint32(b[28:32])
	c.f.SegmentCount = binary.BigEndian.Uint32(b[32:36])
	c.f.SegmentCountLength = binary.BigEndian.Uint32(b[36:40])
	fLen := binary.BigEndian.Uint64(b[40:48])
	c.f.Fingerprints = make([]uint8, fLen)
	copy(c.f.Fingerprints, b[48:])
	return nil
}

func (c *ConnectedClients) Has(client uint64) bool {
	return c.f.Contains(client)
}
