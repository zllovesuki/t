package multiplexer

import (
	"errors"
	"time"

	"go.uber.org/zap"
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
