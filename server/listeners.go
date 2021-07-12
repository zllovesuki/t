package server

import (
	"context"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"

	"go.uber.org/zap"
)

func (s *Server) clientListener(ctx context.Context, p *multiplexer.Peer) {
	go p.Start(ctx)
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
}

func (s *Server) peerListeners(ctx context.Context, p *multiplexer.Peer) {
	go p.Start(ctx)
	go func() {
		<-p.NotifyClose()
		s.logger.Info("removing disconnected peer", zap.Uint64("peerID", p.Peer()))
		s.notifier.Remove(p.Peer())
		s.remoteClients.RemovePeer(p.Peer())
		s.peers.Remove(p.Peer())
	}()
	go func() {
		for c := range p.Handle(ctx) {
			if c.Pair.Destination == 0 && c.Pair.Source == 0 {
				ch := s.notifier.Put(p.Peer(), c.Conn)
				go s.handleNotify(ctx, ch)
				continue
			}
			s.Open(ctx, c.Conn, c.Pair)
		}
	}()
	go func() {
		s.logger.Info("outgoing notify stream", zap.Uint64("peerID", p.Peer()))
		c, err := p.OpenNotify()
		if err != nil {
			s.logger.Error("opening outgoing notify stream", zap.Uint64("peerID", p.Peer()), zap.Error(err))
			return
		}
		ch := s.notifier.Put(p.Peer(), c)
		go s.handleNotify(ctx, ch)
	}()
}

func (s *Server) handleNotify(ctx context.Context, f func() (uint64, <-chan []byte)) {
	peer, ch := f()
	for b := range ch {
		// detected via data race
		b := b
		var x state.ClientUpdate
		x.Unpack(b)

		switch x.Connected {
		case true:
			s.remoteClients.Put(x.Client, x.Peer)
		case false:
			s.remoteClients.RemoveClient(x.Client)
		}
	}
	s.logger.Info("existing notify stream", zap.Uint64("peerID", peer))
}
