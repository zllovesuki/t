package server

import (
	"sync/atomic"

	"github.com/zllovesuki/t/state"

	"go.uber.org/zap"
)

type cachedValue struct {
	connectedClients atomic.Value
}

func (s *Server) backgroundDispatcher() {
	s.logger.Debug("background: starting dispatcher")
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-s.clientEvtCh:
			s.generateConnectedClients()
		}
	}
}

func (s *Server) generateConnectedClients() {
	c, err := state.NewConnectedClients(s.PeerID(), s.clients.Snapshot())
	if err != nil {
		s.logger.Error("generating ConnectedClients", zap.Error(err))
		return
	}
	b, err := c.MarshalBinary()
	if err != nil {
		s.logger.Error("marshalling ConnectedClients as binary", zap.Error(err))
	}
	s.cached.connectedClients.Store(b)
	s.logger.Info("background: storing new ConnectedClients", zap.Uint64("CRC64", c.CRC64))
}
