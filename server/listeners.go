package server

import (
	"context"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"

	"go.uber.org/zap"
)

func (s *Server) handlePeerEvents(ctx context.Context) {
	for peer := range s.peers.Notify() {
		go peer.Start(ctx)
		go func(p *multiplexer.Peer) {
			for c := range p.Handle(ctx) {
				if err := s.Forward(ctx, c.Conn, c.Pair); err != nil {
					s.logger.Error("forwarding stream", zap.Error(err), zap.Uint64("peerID", p.Peer()), zap.Any("paid", c.Pair))
				}
			}
			s.logger.Debug("exiting peer streams event", zap.Uint64("peerID", p.Peer()))
		}(peer)
		s.peerGraph.AddEdges(peer.Peer(), s.PeerID())
	}
}

func (s *Server) handleClientEvents(ctx context.Context) {
	for peer := range s.clients.Notify() {
		go func(p *multiplexer.Peer) {
			s.peerGraph.AddEdges(p.Peer(), s.PeerID())
		}(peer)
		go func(p *multiplexer.Peer) {
			<-p.NotifyClose()
			s.logger.Debug("removing disconnected client", zap.Uint64("peerID", p.Peer()))
			s.clients.Remove(p.Peer())
			s.peerGraph.RemovePeer(p.Peer())
		}(peer)
		// do not handle forward request from client
		// go p.Start(ctx)
	}
}

func (s *Server) handleMerge(ctx context.Context) {
	for x := range s.updatesCh {
		// because of the properties of our design:
		// 1. client PeerIDs are unique across sessions
		// 2. only a maximum of single hop is allowed inter-peers
		// thus, we can replace our peer's peer graph with the incoming one
		// if c.Ring() differs from the ring in our peer graph.
		go func(u *state.ConnectedClients) {
			s.logger.Debug("push/pull: processing state transfer", zap.Uint64("Peer", u.Peer))
			if s.peerGraph.Replace(u) {
				s.logger.Debug("push/pull: pear graph was updated", zap.Uint64("Peer", u.Peer))
			}
		}(x)
	}
}
