package server

import "encoding/json"

type Meta struct {
	Multiplexer string
	PeerID      int64
}

func (m *Meta) Pack() []byte {
	x, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return x
}

func (m *Meta) Unpack(b []byte) error {
	var x Meta
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	*m = x
	return nil
}
