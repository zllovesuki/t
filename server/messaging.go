package server

import (
	"fmt"

	"github.com/zllovesuki/t/messaging"

	"go.uber.org/zap"
)

func (s *Server) handlePeerMessaging(ch <-chan messaging.Message, peer uint64) {
	defer func() {
		s.logger.Info("exiting messaging handler", zap.Uint64("peer", peer))
	}()
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			fmt.Printf("%+v\n", m)
		}
	}
}
