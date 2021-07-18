package messaging

import (
	"encoding/binary"
	"net"

	"github.com/pkg/errors"
)

func (m *Message) Pack() []byte {
	dl := len(m.Data)
	buf := make([]byte, messageHeaderLength+dl)
	binary.BigEndian.PutUint64(buf[0:8], uint64(dl))
	binary.BigEndian.PutUint64(buf[8:16], uint64(m.Type))
	binary.BigEndian.PutUint64(buf[16:messageHeaderLength], m.From)
	copy(buf[messageHeaderLength:], m.Data)
	return buf
}

func (m *Message) Unpack(b []byte) {
	dl := binary.BigEndian.Uint64(b[0:8])
	m.Type = MessageType(binary.BigEndian.Uint64(b[8:16]))
	m.From = binary.BigEndian.Uint64(b[16:messageHeaderLength])
	m.Data = make([]byte, dl)
	copy(m.Data, b[messageHeaderLength:])
}

func (m *Message) ReadFrom(conn net.Conn) error {
	head := make([]byte, messageHeaderLength)
	l, err := conn.Read(head)
	if err != nil {
		return errors.Wrap(err, "reading message header")
	}
	if l != messageHeaderLength {
		return errors.New("mismatched header length")
	}

	m.Type = MessageType(binary.BigEndian.Uint64(head[8:16]))
	m.From = binary.BigEndian.Uint64(head[16:messageHeaderLength])
	dl := int(binary.BigEndian.Uint64(head[0:8]))
	m.Data = make([]byte, dl)

	l, err = conn.Read(m.Data)
	if err != nil {
		return errors.Wrap(err, "reading message data")
	}
	if l != dl {
		return errors.New("incomplete data buffer read")
	}
	return nil
}
