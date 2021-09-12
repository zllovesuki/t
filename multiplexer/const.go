package multiplexer

import (
	"fmt"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

//go:generate stringer -type=Protocol

type Protocol int

const (
	UnknownProtocol Protocol = iota
	YamuxProtocol
	MplexProtocol
	QUICProtocol
)

var AcceptableTLSProtocols = []Protocol{
	YamuxProtocol,
	MplexProtocol,
}

var AcceptableQUICProtocols = []Protocol{
	QUICProtocol,
}

var (
	ErrUnknownProtocol = fmt.Errorf("unknown protocol")
)

type Config struct {
	Logger    *zap.Logger
	Conn      interface{}
	Peer      uint64
	Initiator bool
	Wait      time.Duration
}

func (p *Config) Validate() error {
	if p.Logger == nil {
		return errors.New("nil logger is invalid")
	}
	if p.Conn == nil {
		return errors.New("nil conn is invalid")
	}
	if p.Peer == 0 {
		return errors.New("peer cannot be 0")
	}
	return nil
}
