package server

import (
	"encoding/binary"
	"net"

	"github.com/pkg/errors"
)

const (
	MetaSize = 24
)

type Meta struct {
	ConnectIP   string
	ConnectPort uint64
	PeerID      uint64
}

func (m *Meta) Pack() []byte {
	b := make([]byte, MetaSize)
	ip := net.ParseIP(m.ConnectIP)
	copy(b[0:net.IPv4len], []byte(ip)[12:16])
	binary.BigEndian.PutUint64(b[8:16], m.ConnectPort)
	binary.BigEndian.PutUint64(b[16:24], m.PeerID)
	return b
}

func (m *Meta) Unpack(b []byte) error {
	if len(b) != MetaSize {
		return errors.New("meta length mismatched")
	}
	ip := net.IP(b[0:net.IPv4len])
	m.ConnectIP = ip.To4().String()
	m.ConnectPort = binary.BigEndian.Uint64(b[8:16])
	m.PeerID = binary.BigEndian.Uint64(b[16:24])
	return nil
}
