package multiplexer

import (
	"fmt"
	"net"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

//go:generate stringer -type=Protocol

type Protocol int

const (
	YamuxProtocol Protocol = iota
	MplexProtocol
)

var (
	ErrUnknownProtocol = fmt.Errorf("unknown protocol")
)

type Config struct {
	Logger    *zap.Logger
	Conn      net.Conn
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
