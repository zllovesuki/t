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
		switch x.Connected {
		case true:
			s.remoteClients.Put(x.Client, x.Peer)
		case false:
			s.remoteClients.RemoveClient(x.Client)
		}
		s.remoteClients.Print()
	}
}
