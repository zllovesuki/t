package server

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/peer"
	"github.com/zllovesuki/t/state"

	"github.com/hashicorp/memberlist"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (s *Server) Gossip() error {
	m, err := memberlist.Create(s.gossipCfg)
	if err != nil {
		return err
	}

	s.gossip = m

	if len(s.config.Gossip.Members) > 0 {
		c, err := s.gossip.Join(s.config.Gossip.Members)
		if err != nil {
			s.logger.Error("gossip join error", zap.Error(err))
		}
		s.logger.Info("found gossipers", zap.Int("successs", c))
	}

	go func() {
		<-s.parentCtx.Done()
		s.logger.Info("shutting down gossip")
		s.gossip.Leave(time.Second * 5)
		s.gossip.Shutdown()
	}()

	return nil
}

// ======== Membership Changes ========

var _ memberlist.EventDelegate = &Server{}

func (s *Server) NotifyJoin(node *memberlist.Node) {
	if node.Name == s.gossipCfg.Name {
		s.logger.Debug("gossip: ignore new node join on ourself")
		return
	}
	if node.Meta == nil {
		return
	}

	var m Meta
	if err := m.Unpack(node.Meta); err != nil {
		return
	}

	logger := s.logger.With(zap.Any("meta", m))

	if m.PeerID == s.PeerID() {
		logger.Fatal("gossip: new peer has the same ID as current node", zap.Uint64("self", s.PeerID()))
	}

	s.logger.Info("gossip: new peer discovered via gossip")

	if s.meta.RespondOnly {
		logger.Info("gossip: current peer is configured to respond connections only, acting as responder")
		return
	}

	if !m.RespondOnly && m.PeerID < s.PeerID() {
		logger.Info("gossip: new peer has a lower PeerID, acting as responder")
		go func(m Meta) {
			time.Sleep(time.Second * 10)

			if s.peers.Get(m.PeerID) != nil {
				return
			}
			logger.Warn("gossip: new peer not connected after 10 seconds, attempt to reach out")
			go s.connectPeer(m)
		}(m)
		return
	}

	go s.connectPeer(m)
}

func (s *Server) NotifyLeave(node *memberlist.Node) {
	if node.Name == s.gossipCfg.Name {
		s.logger.Debug("gossip: ignore node leave on ourself")
		return
	}

	var m Meta
	if err := m.Unpack(node.Meta); err != nil {
		return
	}

	s.logger.Info("dead peer discovered via gossip", zap.Any("meta", m))

	s.removePeer(m.PeerID)
}

func (s *Server) NotifyUpdate(node *memberlist.Node) {
}

// ======== Data Exchange ========

var _ memberlist.Delegate = &Server{}

func (s *Server) NodeMeta(limit int) []byte {
	return s.metaBytes.Load().([]byte)
}

type ACMESynchronization struct {
	AccountFile *acme.AccountFile
	Bundle      *acme.Bundle
}

func (s *Server) LocalState(join bool) []byte {
	if join {
		s.logger.Info("gossip: new peer joining, sending acme synchronization details")
		af, err := s.certManager.ExportAccount()
		if err != nil {
			s.logger.Error("gossip: exporting acme account for synchronization", zap.Error(err))
			return nil
		}
		bundle, err := s.certManager.ExportBundle()
		if err != nil {
			s.logger.Error("gossip: exporting acme bundle for synchronization", zap.Error(err))
			return nil
		}
		as := ACMESynchronization{
			AccountFile: af,
			Bundle:      bundle,
		}
		b, err := json.Marshal(&as)
		if err != nil {
			s.logger.Error("gossip: marshaling acme synchronization", zap.Error(err))
			return nil
		}
		return b
	}
	c := state.ConnectedClients{
		Peer:    s.PeerID(),
		Clients: s.clients.Snapshot(),
	}
	return c.Pack()
}

func (s *Server) MergeRemoteState(buf []byte, join bool) {
	if len(buf) == 0 {
		return
	}
	if join {
		var as ACMESynchronization
		err := json.Unmarshal(buf, &as)
		if err != nil {
			s.logger.Error("gossip: unmarshaling acme synchronization", zap.Error(err))
			return
		}
		if as.AccountFile == nil || as.Bundle == nil {
			s.logger.Info("gossip: ignoring incomplete acme synchronization")
			return
		}
		s.logger.Info("gossip: received acme synchronization details")
		err = s.certManager.ImportAccount(*as.AccountFile, !s.config.Debug)
		if err != nil {
			if !errors.Is(err, acme.ErrAccountExists) {
				s.logger.Error("gossip: importing acme account from synchronization", zap.Error(err))
				return
			}
		}
		err = s.certManager.ImportBundle(*as.Bundle, !s.config.Debug)
		if err != nil {
			s.logger.Error("gossip: importing acme bundle from synchronization", zap.Error(err))
			return
		}
		return
	}
	var c state.ConnectedClients
	c.Unpack(buf)
	s.updatesCh <- &c
}

func (s *Server) NotifyMsg(msg []byte) {
}

func (s *Server) GetBroadcasts(overhead, limit int) [][]byte {
	return s.broadcasts.GetBroadcasts(overhead, limit)
}

// ======== Gossip Helpers ========

func (s *Server) connectPeer(m Meta) {
	var err error
	var connector interface{}
	var conn net.Conn
	var closer func() = func() {}
	logger := s.logger.With(zap.Any("meta", m))

	link := multiplexer.Link{
		Source:      s.PeerID(),
		Destination: m.PeerID,
		Protocol:    m.Protocol,
	}
	buf := link.Pack()

	logger.Debug("initiating handshake with peer")

	defer func() {
		if err == nil {
			return
		}
		logger.Error("connecting to peer", zap.Error(err))
		if closer != nil {
			closer()
		}
	}()

	connector, conn, closer, err = peer.Dial(peer.DialOptions{
		Protocol: m.Protocol,
		Addr:     fmt.Sprintf("%s:%d", m.ConnectIP, m.ConnectPort),
		TLS:      s.config.PeerTLSConfig,
	})
	if err != nil {
		err = errors.Wrap(err, "dialing peer")
		return
	}

	_, err = conn.Write(buf)
	if err != nil {
		err = errors.Wrap(err, "writing handshake")
		return
	}

	err = s.peers.NewPeer(s.parentCtx, m.Protocol, multiplexer.Config{
		Logger:    logger,
		Conn:      connector,
		Peer:      m.PeerID,
		Initiator: true,
		Wait:      time.Second,
	})
}

func (s *Server) handleMerge() {
	for x := range s.updatesCh {
		if x.Peer == s.PeerID() {
			continue
		}
		// because of the properties of our design:
		// 1. client PeerIDs are unique across sessions
		// 2. only a maximum of single hop is allowed inter-peers
		// thus, we can replace our peer's peer graph with the incoming one
		// if c.Ring() differs from the ring in our peer graph.
		go func(u *state.ConnectedClients) {
			logger := s.logger.With(zap.Uint64("peer", u.Peer))
			logger.Debug("push/pull: processing state transfer")
			if s.peerGraph.Replace(u) {
				logger.Info("push/pull: peer graph was updated")
				logger.Sugar().Debugf("peerGraph at time %s:\n%+v", time.Now().Format(time.RFC3339), s.peerGraph)
			}
		}(x)
	}
}
