//go:generate stringer -type=Protocol -linecomment
package protocol

import (
	"fmt"

	"go.uber.org/zap/zapcore"
)

type Protocol byte

var _ zapcore.ObjectMarshaler = Protocol(0)

func (p Protocol) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("Protocol", p.String())
	return nil
}

const (
	Unknown Protocol = iota // unknown
	Yamux                   // yamux
	Mplex                   // mplex
	QUIC                    // quic
)

var TLSProtos = []Protocol{
	Yamux,
	Mplex,
}

var QUICProtos = []Protocol{
	QUIC,
}

var (
	ErrUnknownProtocol = fmt.Errorf("unknown protocol")
)
