package server

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/zllovesuki/t/acme"
	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/alpn"
	"github.com/zllovesuki/t/profiler"
	"github.com/zllovesuki/t/state"

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

	go func() {
		s.logger.Info("gossip: reconcilate peers every 1 minute")

		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-s.parentCtx.Done():
				return
			case <-ticker.C:
				s.peerReconciliation()
			}
		}
	}()

	return nil
}

func (s *Server) shouldReachOut(m Meta) bool {
	logger := s.logger.With(zap.Object("meta", m))

	if s.meta.RespondOnly {
		logger.Info("gossip: peer is configured to respond to connections only, acting as responder")
		return false
	}

	if !m.RespondOnly && m.PeerID < s.PeerID() {
		logger.Info("gossip: peer has a lower PeerID, acting as responder")
		return false
	}

	return true
}

func (s *Server) peerReconciliation() {
	var m Meta
	knownNodes := s.gossip.Members()

	for _, node := range knownNodes {
		if node.State != memberlist.StateAlive {
			continue
		}
		if node.Name == s.gossipCfg.Name {
			continue
		}
		if err := m.UnmarshalBinary(node.Meta); err != nil {
			continue
		}
		if m.PeerID == s.PeerID() {
			continue
		}
		if s.peers.Get(m.PeerID) == nil && s.shouldReachOut(m) {
			s.logger.Info("gossip: dropped peer detected, reconnecting", zap.Object("meta", m))
			go s.connectPeer(m)
		}
	}
}

// ======== Membership Changes ========

var _ memberlist.EventDelegate = &Server{}

func (s *Server) NotifyJoin(node *memberlist.Node) {
	if node.Name == s.gossipCfg.Name {
		s.logger.Debug("gossip: ignore new node join on ourself")
		return
	}

	var m Meta
	if err := m.UnmarshalBinary(node.Meta); err != nil {
		return
	}

	s.logger.Info("gossip: new peer discovered via gossip", zap.Object("meta", m))

	if m.PeerID == s.PeerID() {
		s.logger.Fatal("gossip: new peer has the same ID as current node", zap.Uint64("self", s.PeerID()))
	}

	if s.shouldReachOut(m) {
		go s.connectPeer(m)
	}
}

func (s *Server) NotifyLeave(node *memberlist.Node) {
	if node.Name == s.gossipCfg.Name {
		s.logger.Debug("gossip: ignore node leave on ourself")
		return
	}

	var m Meta
	if err := m.UnmarshalBinary(node.Meta); err != nil {
		return
	}

	s.logger.Info("dead peer discovered via gossip", zap.Object("meta", m))

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
	From        uint64
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
			From:        s.PeerID(),
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

	crc := s.clients.CRC64()
	ext, found := s.stateCache.Get(crc)
	if found {
		s.logger.Debug("stateCache: reusing cached ConnectedClients", zap.Uint64("crc64", crc))
		return ext.([]byte)
	} else {
		c := state.NewConnectedClients(s.PeerID(), s.clients.Snapshot())
		b, _ := c.MarshalBinary()
		s.stateCache.Set(c.CRC64, b, int64(len(b)))
		s.logger.Debug("stateCache: insert new ConnectedClients cache entry", zap.Uint64("crc64", c.CRC64))
		return b
	}
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
		if as.From == s.PeerID() {
			s.logger.Info("gossip: ignoring acme synchronization from ourself")
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
	if err := c.UnmarshalBinary(buf); err != nil {
		s.logger.Error("gossip: error unmarshaling connected clients buffer", zap.Error(err))
		return
	}
	s.updatesCh <- c
}

func (s *Server) NotifyMsg(msg []byte) {
}

func (s *Server) GetBroadcasts(overhead, limit int) [][]byte {
	return s.broadcasts.GetBroadcasts(overhead, limit)
}

// ======== Ping ========

var _ memberlist.PingDelegate = &Server{}

func (s *Server) AckPayload() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(s.peers.Len()))
	return b
}

func (s *Server) NotifyPingComplete(other *memberlist.Node, rtt time.Duration, payload []byte) {
	var m Meta
	if err := m.UnmarshalBinary(other.Meta); err != nil {
		s.logger.Error("unmarshal node meta from ping", zap.Error(err))
		return
	}

	n := binary.BigEndian.Uint64(payload)
	s.logger.Debug("ping", zap.Uint64("Peer", m.PeerID), zap.Duration("rtt", rtt), zap.Uint64("numPeers", n))
	profiler.PeerLatencyHist.Observe(float64(rtt / time.Millisecond))
}

// ======== Gossip Helpers ========

func (s *Server) connectPeer(m Meta) {
	var err error
	var connector interface{}
	var conn net.Conn
	var closer func() = func() {}
	var buf []byte

	logger := s.logger.With(zap.Object("meta", m))

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

	link := multiplexer.Link{
		Source:      s.PeerID(),
		Destination: m.PeerID,
		Protocol:    m.Protocol,
		ALPN:        alpn.Multiplexer,
	}

	buf, err = link.MarshalBinary()
	if err != nil {
		err = fmt.Errorf("marshal link: %w", err)
		return
	}

	dialer, err := multiplexer.Dialer(m.Protocol)
	if err != nil {
		err = fmt.Errorf("getting dialer: %w", err)
		return
	}

	connector, conn, closer, err = dialer(fmt.Sprintf("%s:%d", m.ConnectIP, m.ConnectPort), s.config.PeerTLSConfig)
	if err != nil {
		err = fmt.Errorf("dialing peer: %w", err)
		return
	}

	_, err = conn.Write(buf)
	if err != nil {
		err = fmt.Errorf("writing handshake: %w", err)
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
		// if crc64 differs from the crc64 in our peer graph.
		go func(u state.ConnectedClients) {
			logger := s.logger.With(zap.Uint64("peer", u.Peer))
			logger.Debug("push/pull: processing state transfer")
			if s.peerGraph.Replace(u) {
				logger.Info("push/pull: peer graph was updated")
			}
		}(x)
	}
}
