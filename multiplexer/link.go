package multiplexer

import (
	"encoding"
	"encoding/binary"
	"fmt"

	"github.com/zllovesuki/t/multiplexer/alpn"
	"github.com/zllovesuki/t/multiplexer/protocol"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// LinkSize is the encoded buffer size of Link for wire format
	LinkSize = 18
)

// MessagingLink defines Link in which implementation should consider as Messaing channel
var MessagingLink = Link{}

// Link defines a bidirectional source and target. It can be used for negotiations on protocol
// during initial handshake
type Link struct {
	Source      uint64
	Destination uint64
	Protocol    protocol.Protocol // used for initial multiplexer handshake only
	ALPN        alpn.ALPN         // used to specify the expected application protocol
}

var _ zapcore.ObjectMarshaler = &Link{}
var _ encoding.BinaryMarshaler = &Link{}
var _ encoding.BinaryUnmarshaler = &Link{}

func (l Link) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddUint64("Source", l.Source)
	enc.AddUint64("Destination", l.Destination)
	zap.Inline(l.Protocol).AddTo(enc)
	zap.Inline(l.ALPN).AddTo(enc)
	return nil
}

func (s *Link) MarshalBinary() ([]byte, error) {
	b := make([]byte, LinkSize)
	binary.BigEndian.PutUint64(b[0:8], s.Source)
	binary.BigEndian.PutUint64(b[8:16], s.Destination)
	b[16] = byte(s.Protocol)
	b[17] = byte(s.ALPN)
	return b, nil
}

func (s *Link) UnmarshalBinary(b []byte) error {
	if len(b) != LinkSize {
		return fmt.Errorf("invalid buffer length: %d", len(b))
	}
	s.Source = binary.BigEndian.Uint64(b[0:8])
	s.Destination = binary.BigEndian.Uint64(b[8:16])
	s.Protocol = protocol.Protocol(b[16])
	s.ALPN = alpn.ALPN(b[17])
	return nil
}

func (s *Link) Flip() Link {
	p := *s
	p.Destination = s.Source
	p.Source = s.Destination
	return p
}
