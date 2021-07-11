package multiplexer

import "encoding/binary"

type Pair struct {
	Source      int64
	Destination int64
}

func (s *Pair) Pack() []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b[0:8], uint64(s.Source))
	binary.BigEndian.PutUint64(b[8:16], uint64(s.Destination))
	return b
}

func (s *Pair) Unpack(b []byte) {
	s.Source = int64(binary.BigEndian.Uint64(b[0:8]))
	s.Destination = int64(binary.BigEndian.Uint64(b[8:16]))
}

func (s *Pair) Flip() Pair {
	p := *s
	p.Destination = s.Source
	p.Source = s.Destination
	return p
}
