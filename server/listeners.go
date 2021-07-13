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
			s.logger.Info("exiting peer streams event", zap.Uint64("peerID", p.Peer()))
		}(peer)
		s.peerGraph.AddPeer(peer.Peer())
		s.peerGraph.AddEdge(peer.Peer(), s.PeerID())
	}
}

func (s *Server) handleClientEvents(ctx context.Context) {
	for peer := range s.clients.Notify() {
		go func(p *multiplexer.Peer) {
			update := &state.ClientUpdate{
				Peer:      s.PeerID(),
				Client:    p.Peer(),
				Connected: true,
				Counter:   0,
			}
			s.broadcasts.QueueBroadcast(update)
			s.peerGraph.AddPeer(peer.Peer())
			s.peerGraph.AddEdge(peer.Peer(), s.PeerID())
		}(peer)
		go func(p *multiplexer.Peer) {
			update := &state.ClientUpdate{
				Peer:      s.PeerID(),
				Client:    p.Peer(),
				Connected: false,
				Counter:   0,
			}
			for {
				select {
				case <-ctx.Done():
					s.broadcasts.QueueBroadcast(update)
					return
				case <-p.NotifyClose():
					s.logger.Info("removing disconnected client", zap.Uint64("peerID", p.Peer()))
					s.clients.Remove(p.Peer())
					s.broadcasts.QueueBroadcast(update)
					s.peerGraph.RemovePeer(p.Peer())
					return
				}
			}
		}(peer)
		// do not handle forward request from client
		// go p.Start(ctx)
	}
}

func (s *Server) handleNotify(ctx context.Context) {
	for x := range s.updatesCh {
		s.logger.Info("gossip: client update", zap.Any("update", x))
		if x.Peer != s.PeerID() {
			switch x.Connected {
			case true:
				if !s.peerGraph.HasPeer(x.Client) {
					s.peerGraph.AddPeer(x.Client)
					s.peerGraph.AddEdge(x.Peer, x.Client)
					x.Counter++
				}
			case false:
				if s.peerGraph.HasPeer(x.Client) {
					s.peerGraph.RemovePeer(x.Client)
					x.Counter++
				}
			}
		}
		if x.Counter < uint64(s.peers.Len()) {
			b := x
			s.broadcasts.QueueBroadcast(&b)
		}
	}
}
