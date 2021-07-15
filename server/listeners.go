package server

import (
	"context"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"

	"go.uber.org/zap"
)

func (s *Server) handlePeerEvents(ctx context.Context) {
	for peer := range s.peers.Notify() {
		// update our peer graph in a different goroutine with closure
		// as this may block
		go func(p *multiplexer.Peer) {
			s.peerGraph.AddEdges(p.Peer(), s.PeerID())
		}(peer)
		// listen for request for forwarding from peers
		go func(p *multiplexer.Peer) {
			for c := range p.Handle(ctx) {
				s.Forward(ctx, c.Conn, c.Pair)
			}
			s.logger.Debug("exiting peer streams event", zap.Uint64("peerID", p.Peer()))
		}(peer)
		// handle forward request from client
		go peer.Start(ctx)
	}
}

func (s *Server) handleClientEvents(ctx context.Context) {
	for peer := range s.clients.Notify() {
		// update our peer graph with the client as this may block with
		// many clients connecting
		go func(p *multiplexer.Peer) {
			s.peerGraph.AddEdges(p.Peer(), s.PeerID())
		}(peer)
		// remove the client once they are disconnected. relying on notify
		// as clients do not partipate in gossips
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
				s.logger.Debug("push/pull: peer graph was updated", zap.Uint64("Peer", u.Peer))
				s.logger.Sugar().Debugf("peerGraph at time %s:%+v", time.Now().Format(time.RFC3339), s.peerGraph)
			}
		}(x)
	}
}
