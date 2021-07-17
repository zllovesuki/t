package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"

	"github.com/hashicorp/memberlist"
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

	if m.PeerID == s.PeerID() {
		s.logger.Fatal("gossip: new peer has the same ID as current node", zap.Any("meta", m), zap.Uint64("self", s.PeerID()))
	}

	s.logger.Info("gossip: new peer discovered via gossip", zap.Any("meta", m))

	if m.PeerID < s.PeerID() {
		s.logger.Info("gossip: new peer has a lower PeerID, acting as responder")
		go func(m Meta) {
			time.Sleep(time.Second * 10)

			if s.peers.Get(m.PeerID) != nil {
				return
			}
			s.logger.Warn("gossip: new peer not connected after 10 seconds, attempt to reach out", zap.Any("meta", m))
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
}

func (s *Server) NotifyUpdate(node *memberlist.Node) {
}

// ======== Data Exchange ========

var _ memberlist.Delegate = &Server{}

func (s *Server) NodeMeta(limit int) []byte {
	return s.Meta()
}

func (s *Server) LocalState(join bool) []byte {
	if join {
		return nil
	}
	c := state.ConnectedClients{
		Peer:    s.PeerID(),
		Clients: s.clients.Snapshot(),
	}
	return c.Pack()
}

func (s *Server) MergeRemoteState(buf []byte, join bool) {
	if len(buf) == 0 || join {
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
	var conn *tls.Conn
	logger := s.logger.With(zap.Any("meta", m))

	conn, err = tls.DialWithDialer(&net.Dialer{
		Timeout: time.Second * 3,
	}, "tcp", fmt.Sprintf("%s:%d", m.ConnectIP, m.ConnectPort), s.config.PeerTLSConfig)
	if err != nil {
		logger.Error("opening tls connection", zap.Error(err))
		return
	}

	defer func() {
		if err == nil {
			return
		}
		logger.Error("connecting to peer", zap.Error(err))
		conn.Close()
	}()

	logger.Debug("initiating handshake with peer")

	pair := multiplexer.Pair{
		Source:      s.PeerID(),
		Destination: m.PeerID,
	}
	buf := pair.Pack()
	conn.Write(buf)

	err = s.peers.NewPeer(s.parentCtx, state.PeerConfig{
		Conn:      conn,
		Peer:      m.PeerID,
		Initiator: true,
		Wait:      time.Second,
	})
}

func (s *Server) handleMerge() {
	for x := range s.updatesCh {
		// because of the properties of our design:
		// 1. client PeerIDs are unique across sessions
		// 2. only a maximum of single hop is allowed inter-peers
		// thus, we can replace our peer's peer graph with the incoming one
		// if c.Ring() differs from the ring in our peer graph.
		go func(u *state.ConnectedClients) {
			s.logger.Debug("push/pull: processing state transfer", zap.Uint64("Peer", u.Peer))
			if s.peerGraph.Replace(u) {
				s.logger.Info("push/pull: peer graph was updated", zap.Uint64("Peer", u.Peer))
				s.logger.Sugar().Debugf("peerGraph at time %s:\n%+v", time.Now().Format(time.RFC3339), s.peerGraph)
			}
		}(x)
	}
}
