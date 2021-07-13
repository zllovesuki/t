package server

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"

	"github.com/hashicorp/memberlist"
	"go.uber.org/zap"
)

func (s *Server) Gossip(ctx context.Context) error {
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
		<-ctx.Done()
		s.logger.Info("shutdowning gossip")
		s.gossip.Leave(time.Second * 5)
		s.gossip.Shutdown()
	}()

	return nil
}

// ======== Membership Changes ========

var _ memberlist.EventDelegate = &Server{}

func (s *Server) NotifyJoin(node *memberlist.Node) {
	if node.Name == fmt.Sprint(s.PeerID()) {
		s.logger.Info("gossip ourself, refusing")
		return
	}
	if node.Meta == nil {
		return
	}

	var m Meta
	if err := m.Unpack(node.Meta); err != nil {
		return
	}

	s.logger.Info("new peer discovered via gossip", zap.Any("meta", m))

	if s.peers.Has(m.PeerID) {
		return
	}
	go s.connectPeer(s.parentCtx, m)
}

func (s *Server) NotifyLeave(node *memberlist.Node) {
	if node.Name == fmt.Sprint(s.PeerID()) {
		s.logger.Info("gossip ourself, refusing")
		return
	}

	var m Meta
	if err := m.Unpack(node.Meta); err != nil {
		// fmt.Printf("error unpacking node meta: %+v\n", err)
		return
	}

	s.logger.Info("dead peer discovered via gossip", zap.Any("meta", m))

	go s.removePeer(s.parentCtx, m)
}

func (s *Server) NotifyUpdate(node *memberlist.Node) {
}

// ======== Data Exchange ========

var _ memberlist.Delegate = &Server{}

func (s *Server) NodeMeta(limit int) []byte {
	return s.Meta()
}

func (s *Server) LocalState(join bool) []byte {
	c := state.ConnectedClients{
		Peer:    s.PeerID(),
		Clients: s.clients.Snapshot(),
	}
	return c.Pack()
}

func (s *Server) MergeRemoteState(buf []byte, join bool) {
	var c state.ConnectedClients
	c.Unpack(buf)
	s.peerGraph.Merge(c)
}

func (s *Server) NotifyMsg(msg []byte) {
	var c state.ClientUpdate
	c.Unpack(msg)
	s.updatesCh <- c
}

func (s *Server) GetBroadcasts(overhead, limit int) [][]byte {
	return s.broadcasts.GetBroadcasts(overhead, limit)
}

// ======== Gossip Helpers ========

func (s *Server) checkRetry(ctx context.Context, m Meta) {
	if !s.peers.Has(m.PeerID) {
		if m.retry > 2 {
			s.logger.Error("handshake retry attempts exhausted", zap.Any("meta", m))
			return
		}
		time.Sleep(time.Second * time.Duration(rand.Intn(5)+1))
		m.retry++
		s.logger.Warn("peer handshake deadlock detected, retrying", zap.Any("meta", m))
		go s.connectPeer(ctx, m)
	}
}

func (s *Server) connectPeer(ctx context.Context, m Meta) {
	var err error
	var conn net.Conn
	logger := s.logger.With(zap.Any("meta", m))

	conn, err = net.Dial("tcp", m.Multiplexer)
	if err != nil {
		logger.Error("opening tcp connection", zap.Error(err))
		return
	}

	defer func() {
		if err == nil {
			return
		}
		logger.Error("connecting to peer", zap.Error(err))
		conn.Close()
		s.checkRetry(ctx, m)
	}()

	logger.Info("initiating handshake with peer")

	pair := multiplexer.Pair{
		Source:      s.PeerID(),
		Destination: m.PeerID,
	}
	buf := pair.Pack()
	conn.Write(buf)

	err = s.peers.NewPeer(ctx, state.PeerConfig{
		Conn:      conn,
		Peer:      m.PeerID,
		Initiator: true,
		Wait:      time.Second * time.Duration(rand.Intn(3)+1),
	})
}

func (s *Server) removePeer(ctx context.Context, m Meta) {
	p := s.peers.Get(m.PeerID)
	if p == nil {
		s.logger.Error("removing a non-existent peer")
		return
	}
	s.logger.Info("removing disconnected peer", zap.Uint64("peerID", p.Peer()))
	s.peers.Remove(p.Peer())
	s.peerGraph.RemovePeer(p.Peer())
}
