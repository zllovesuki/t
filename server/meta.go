package server

import (
	"encoding"
	"encoding/binary"
	"net"

	"github.com/zllovesuki/t/multiplexer/protocol"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	MetaSize = 26
)

type Meta struct {
	ConnectIP   string
	ConnectPort uint64
	PeerID      uint64
	Protocol    protocol.Protocol
	RespondOnly bool
}

var _ zapcore.ObjectMarshaler = &Meta{}
var _ encoding.BinaryMarshaler = &Meta{}
var _ encoding.BinaryUnmarshaler = &Meta{}

func (m Meta) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("IP", m.ConnectIP)
	enc.AddUint64("Port", m.ConnectPort)
	enc.AddUint64("Peer", m.PeerID)
	zap.Inline(m.Protocol).AddTo(enc)
	enc.AddBool("RespondeOnly", m.RespondOnly)
	return nil
}

func (m *Meta) MarshalBinary() ([]byte, error) {
	b := make([]byte, MetaSize)
	ip := net.ParseIP(m.ConnectIP)
	copy(b[0:net.IPv4len], []byte(ip)[12:16])
	binary.BigEndian.PutUint64(b[8:16], m.ConnectPort)
	binary.BigEndian.PutUint64(b[16:24], m.PeerID)
	b[24] = byte(m.Protocol)
	if m.RespondOnly {
		b[25] = 1
	}
	return b, nil
}

func (m *Meta) UnmarshalBinary(b []byte) error {
	if len(b) != MetaSize {
		return errors.Errorf("invalid buffer length: %d", len(b))
	}
	ip := net.IP(b[0:net.IPv4len])
	m.ConnectIP = ip.To4().String()
	m.ConnectPort = binary.BigEndian.Uint64(b[8:16])
	m.PeerID = binary.BigEndian.Uint64(b[16:24])
	m.Protocol = protocol.Protocol(b[24])
	if b[25] == 1 {
		m.RespondOnly = true
	}
	return nil
}
