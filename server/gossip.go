package server

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"

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

var _ memberlist.EventDelegate = &Server{}

func (s *Server) NotifyJoin(node *memberlist.Node) {
	if node.Name == fmt.Sprint(s.PeerID()) {
		s.logger.Info("gossip outself, refusing")
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

	go s.connectPeer(s.parentCtx, m)
}

func (s *Server) NotifyLeave(node *memberlist.Node) {
	if node.Name == fmt.Sprint(s.PeerID()) {
		s.logger.Info("gossip outself, refusing")
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

func (s *Server) NodeMeta(limit int) []byte {
	return s.Meta()
}
func (s *Server) LocalState(join bool) []byte {
	return nil
}
func (s *Server) NotifyMsg(msg []byte) {
}
func (s *Server) GetBroadcasts(overhead, limit int) [][]byte {
	return nil
}
func (s *Server) MergeRemoteState(buf []byte, join bool) {
}

func (s *Server) connectPeer(ctx context.Context, m Meta) {
	var err error
	var conn net.Conn
	logger := s.logger.With(zap.Any("meta", m))
	defer func() {
		if err == nil {
			return
		}
		logger.Error("connecting to peer", zap.Error(err))
	}()

	conn, err = net.Dial("tcp", m.Multiplexer)
	if err != nil {
		return
	}

	logger.Info("initiating handshake with peer")

	pair := multiplexer.Pair{
		Source:      s.PeerID(),
		Destination: m.PeerID,
	}
	buf := pair.Pack()
	conn.Write(buf)

	var p *multiplexer.Peer
	p, err = s.peers.NewPeer(ctx, conn, pair.Destination, true)
	if err != nil {
		return
	}

	go s.peerListeners(ctx, p)
}

func (s *Server) removePeer(ctx context.Context, m Meta) {
	s.peers.Remove(m.PeerID)
}
