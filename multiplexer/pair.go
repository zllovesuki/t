package multiplexer

import "encoding/binary"

const (
	PairSize = 16
)

type Pair struct {
	Source      uint64
	Destination uint64
}

func (s *Pair) Pack() []byte {
	b := make([]byte, PairSize)
	binary.BigEndian.PutUint64(b[0:8], s.Source)
	binary.BigEndian.PutUint64(b[8:16], s.Destination)
	return b
}

func (s *Pair) Unpack(b []byte) {
	s.Source = binary.BigEndian.Uint64(b[0:8])
	s.Destination = binary.BigEndian.Uint64(b[8:16])
}

func (s *Pair) Flip() Pair {
	p := *s
	p.Destination = s.Source
	p.Source = s.Destination
	return p
}
