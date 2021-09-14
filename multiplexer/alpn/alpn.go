//go:generate stringer -type=ALPN -linecomment
package alpn

import (
	"fmt"

	"go.uber.org/zap/zapcore"
)

type ALPN byte

var _ zapcore.ObjectMarshaler = ALPN(0)

func (a ALPN) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("ALPN", a.String())
	return nil
}

const (
	Unknown     ALPN = iota //
	Multiplexer             // multiplexer
	HTTP                    // http/1.1
	Raw                     // raw
)

var (
	ErrUnknownALPN = fmt.Errorf("unknown ALPN proto")
)

var Map = map[ALPN]string{
	Unknown:     Unknown.String(),
	Multiplexer: Multiplexer.String(),
	HTTP:        HTTP.String(),
	Raw:         Raw.String(),
}

var ReverseMap = func() (m map[string]ALPN) {
	m = make(map[string]ALPN)
	for k, v := range Map {
		m[v] = k
	}
	return
}()

var Protos = func() (s []string) {
	s = make([]string, 0)
	for _, v := range Map {
		s = append(s, v)
	}
	return
}()
