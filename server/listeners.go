package server

import (
	"context"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"

	"go.uber.org/zap"
)

func (s *Server) handlePeerConnects(ctx context.Context) {
	for m := range s.peerConnectQueue {
		if s.peers.Has(m.PeerID) {
			continue
		}
		s.connectPeer(ctx, m)
	}
}

func (s *Server) handlePeerEvents(ctx context.Context) {
	for p := range s.peers.Notify() {
		p := p
		go p.Start(ctx)
		go func(p *multiplexer.Peer) {
			s.logger.Info("opening outgoing notify stream", zap.Uint64("peerID", p.Peer()))
			c, err := p.OpenNotify()
			if err != nil {
				s.logger.Error("opening outgoing notify stream", zap.Uint64("peerID", p.Peer()), zap.Error(err))
				return
			}
			ch := s.notifier.Put(p.Peer(), c)
			s.handleNotify(ctx, ch, p.Peer(), false)
		}(p)
		go func(p *multiplexer.Peer) {
			for c := range p.Handle(ctx) {
				if c.Pair.Destination == 0 && c.Pair.Source == 0 {
					s.logger.Info("processing incoming notify stream", zap.Uint64("peerID", p.Peer()))
					ch := s.notifier.Put(p.Peer(), c.Conn)
					go s.handleNotify(ctx, ch, p.Peer(), true)
					continue
				}
				s.Open(ctx, c.Conn, c.Pair)
			}
			s.logger.Info("exiting peer streams event", zap.Uint64("peerID", p.Peer()))
		}(p)
	}
}

func (s *Server) handleClientEvents(ctx context.Context) {
	for p := range s.clients.Notify() {
		p := p
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-p.NotifyClose():
					return
				case <-time.After(time.Second * 10):
					update := state.ClientUpdate{
						Peer:      s.PeerID(),
						Client:    p.Peer(),
						Connected: true,
					}
					s.notifier.Broadcast(update.Pack())
				}
			}
		}()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-p.NotifyClose():
					s.logger.Info("removing disconnected client", zap.Uint64("peerID", p.Peer()))
					s.clients.Remove(p.Peer())
					update := state.ClientUpdate{
						Peer:      s.PeerID(),
						Client:    p.Peer(),
						Connected: false,
					}
					s.notifier.Broadcast(update.Pack())
					return
				}
			}
		}()
		// go p.Start(ctx)
	}
}

func (s *Server) handleNotify(ctx context.Context, ch <-chan state.ClientUpdate, peer uint64, in bool) {
	for x := range ch {
		// s.logger.Info("client update", zap.Any("update", x))
		switch x.Connected {
		case true:
			s.remoteClients.Put(x.Client, x.Peer)
		case false:
			s.remoteClients.RemoveClient(x.Client)
		}
		s.remoteClients.Print()
	}
	s.logger.Info("exiting notify stream", zap.Bool("incoming", in), zap.Uint64("peerID", peer))
}
