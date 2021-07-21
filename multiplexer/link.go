package multiplexer

import "encoding/binary"

const (
	// LinkSize is the encoded buffer size of Link for wire format
	LinkSize = 17
)

// MessagingLink defines Link in which implementation should consider as Messaing channel
var MessagingLink = Link{}

// Link defines a bidirectional source and target. It can be used for negotiations on protocol
// during initial handshake
type Link struct {
	Source      uint64
	Destination uint64
	Protocol    Protocol
}

func (s *Link) Pack() []byte {
	b := make([]byte, LinkSize)
	binary.BigEndian.PutUint64(b[0:8], s.Source)
	binary.BigEndian.PutUint64(b[8:16], s.Destination)
	b[16] = byte(s.Protocol)
	return b
}

func (s *Link) Unpack(b []byte) {
	s.Source = binary.BigEndian.Uint64(b[0:8])
	s.Destination = binary.BigEndian.Uint64(b[8:16])
	s.Protocol = Protocol(b[16])
}

func (s *Link) Flip() Link {
	p := *s
	p.Destination = s.Source
	p.Source = s.Destination
	return p
}
