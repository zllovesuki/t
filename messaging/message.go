package messaging

import (
	"encoding"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type Message struct {
	Type MessageType
	From uint64
	Data []byte
}

var _ encoding.BinaryMarshaler = &Message{}
var _ encoding.BinaryUnmarshaler = &Message{}

func (m *Message) MarshalBinary() ([]byte, error) {
	dl := len(m.Data)
	buf := make([]byte, messageHeaderLength+dl)
	binary.BigEndian.PutUint64(buf[0:8], uint64(dl))
	binary.BigEndian.PutUint64(buf[8:16], uint64(m.Type))
	binary.BigEndian.PutUint64(buf[16:messageHeaderLength], m.From)
	copy(buf[messageHeaderLength:], m.Data)
	return buf, nil
}

func (m *Message) UnmarshalBinary(b []byte) error {
	dl := binary.BigEndian.Uint64(b[0:8])
	m.Type = MessageType(binary.BigEndian.Uint64(b[8:16]))
	m.From = binary.BigEndian.Uint64(b[16:messageHeaderLength])
	m.Data = make([]byte, dl)
	copy(m.Data, b[messageHeaderLength:])
	return nil
}

func (m *Message) ReadFrom(f io.Reader) (int64, error) {
	var x int64

	head := make([]byte, messageHeaderLength)
	l, err := f.Read(head)
	x += int64(l)
	if err != nil {
		return x, fmt.Errorf("reading message header: %w", err)
	}
	if l != messageHeaderLength {
		return x, fmt.Errorf("invalid buffer length: %d", l)
	}

	m.Type = MessageType(binary.BigEndian.Uint64(head[8:16]))
	m.From = binary.BigEndian.Uint64(head[16:messageHeaderLength])
	dl := int(binary.BigEndian.Uint64(head[0:8]))
	m.Data = make([]byte, dl)

	// TODO(zllovesuki): revisit this
	l, err = f.Read(m.Data)
	x += int64(l)

	if err != nil {
		return x, fmt.Errorf("reading message data: %w", err)
	}
	if l != dl {
		return x, errors.New("incomplete data buffer read")
	}
	return x, nil
}
