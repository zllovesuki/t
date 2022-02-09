package server

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/messaging"

	"go.uber.org/zap"
)

func (s *Server) handleMembershipChange() {
	go s.leaderTimer()
	lastLeader := atomic.LoadUint64(&s.currentLeader)
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-s.membershipCh:
			peers := append(s.peers.Snapshot(), s.PeerID())
			lowest := ^uint64(0)
			for _, peer := range peers {
				if peer < lowest {
					lowest = peer
				}
			}
			if !atomic.CompareAndSwapUint64(&s.currentLeader, lowest, lowest) {
				atomic.StoreUint64(&s.currentLeader, lowest)
				s.logger.Info("new leader calculated", zap.Uint64("leader", lowest))
				lastLeader = lowest
				if s.PeerID() == atomic.LoadUint64(&s.currentLeader) {
					s.startLeader <- struct{}{}
				} else {
					s.stopLeader <- struct{}{}
				}
			} else {
				s.logger.Info("current leader unchanged", zap.Uint64("leader", lastLeader))
			}
		}
	}
}

func (s *Server) leaderTimer() {
	var timer <-chan time.Time
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-s.startLeader:
			timer = time.After(time.Second * 15)
		case <-s.stopLeader:
			timer = nil
		case <-timer:
			if s.PeerID() != atomic.LoadUint64(&s.currentLeader) {
				s.stopLeader <- struct{}{}
				continue
			}
			go func() {
				if !s.config.Debug {
					s.logger.Debug("running acme check as leader")
					s.checkACMEAccountKeys()
					s.checkACMECerts()
				}
				if s.PeerID() == atomic.LoadUint64(&s.currentLeader) {
					s.startLeader <- struct{}{}
				}
			}()
		}
	}
}

func (s *Server) checkACMEAccountKeys() {
	af, err := s.certManager.ExportAccount()
	if errors.Is(err, acme.ErrNoAccount) {
		s.logger.Info("leader: no acme account found, try loading from file")
		err = s.certManager.LoadAccountFromFile()
		if errors.Is(err, acme.ErrNoAccount) {
			// truly bootstraping
			s.logger.Info("leader: no acme account found locally, creating a new account")
			err = s.certManager.CreateAccount()
			if err != nil {
				s.logger.Error("leader: creating acme account", zap.Error(err))
				return
			}
		}
		af, err = s.certManager.ExportAccount()
		if err != nil {
			s.logger.Error("leader: export account returned error when creation was successful", zap.Error(err))
			return
		}

		b, err := json.Marshal(&af)
		if err != nil {
			s.logger.Error("leader: marshaling account for announcement", zap.Error(err))
			return
		}

		s.logger.Info("leader: announcing acme account keys")
		s.messaging.Announce(messaging.MessageACMEAccountKey, b)
	}
}

func (s *Server) checkACMECerts() {
	var renewed bool

	bundle, err := s.certManager.ExportBundle()
	if errors.Is(err, acme.ErrNoCert) {
		s.logger.Info("leader: no acme cert found, requesting a new one")
		err = s.certManager.RequestCertificate()
		if err != nil {
			s.logger.Error("leader: requesting acme cert", zap.Error(err))
			return
		}
		bundle, err = s.certManager.ExportBundle()
		s.logger.Info("leader: new acme cert generated")
		renewed = true
	}
	if err != nil {
		s.logger.Error("leader: exporting acme cert", zap.Error(err))
		return
	}

	c, err := s.certManager.GetCertificatesFunc(nil)
	if err != nil {
		s.logger.Error("leader: getCertficates returned error when exporting was successful", zap.Error(err))
		return
	}
	var leaf *x509.Certificate
	for _, cert := range c.Certificate {
		x, err := x509.ParseCertificate(cert)
		if err != nil {
			s.logger.Error("leader: parsing certificate", zap.Error(err))
			return
		}
		for _, name := range x.DNSNames {
			// it is possible that s.config.Domain is not on 443 port
			if strings.Contains(s.config.Domain, name) {
				leaf = x
				break
			}
		}
	}

	if leaf == nil {
		s.logger.Error("leader: no cert containing our domain was found")
		return
	}
	// check in within 30 days of renewal
	if time.Until(leaf.NotAfter) < time.Hour*24*30 {
		s.logger.Info("leader: certificate expiring within 30 days, renewing", zap.Time("expiration", leaf.NotAfter))
		err = s.certManager.RequestCertificate()
		if err != nil {
			s.logger.Error("leader: unable to renew certificate", zap.Error(err))
			return
		}
		bundle, err = s.certManager.ExportBundle()
		if err != nil {
			s.logger.Error("leader: exporting bundle failed when renewal was successful")
			return
		}
		renewed = true
	}

	b, err := json.Marshal(&bundle)
	if err != nil {
		s.logger.Error("leader: marshaling certs bundle for announcement", zap.Error(err))
		return
	}

	if renewed {
		s.logger.Info("leader: announcing new acme certificates bundle")
		s.messaging.Announce(messaging.MessageClientCerts, b)
	}
}
