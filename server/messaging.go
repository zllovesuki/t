package server

import (
	"encoding/json"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/messaging"

	"go.uber.org/zap"
)

func (s *Server) handlePeerMessaging(ch <-chan messaging.Message, peer uint64) {
	defer func() {
		s.logger.Debug("exiting messaging handler", zap.Uint64("peer", peer))
	}()
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			switch m.Type {
			case messaging.MessageACMEAccountKey:
				var af acme.AccountFile
				if err := json.Unmarshal(m.Data, &af); err != nil {
					s.logger.Error("unmarshaling acme account key from announcement", zap.Error(err))
					continue
				}
				if err := s.certManager.ImportAccount(af, !s.config.DisableACME); err != nil {
					s.logger.Error("importing acme account key from announcement", zap.Error(err))
				}
			case messaging.MessageClientCerts:
				var b acme.Bundle
				if err := json.Unmarshal(m.Data, &b); err != nil {
					s.logger.Error("unmarshaling acme bundle from announcement", zap.Error(err))
					continue
				}
				if err := s.certManager.ImportBundle(b, !s.config.DisableACME); err != nil {
					s.logger.Error("importing acme bundle from announcement", zap.Error(err))
				}
			}
		}
	}
}
