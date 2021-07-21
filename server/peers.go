package server

import (
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/state"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (s *Server) peerHandshake(conn net.Conn) {
	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		if errors.Is(err, state.ErrSessionAlreadyEstablished) {
			logger.Info("reusing established session")
			return
		}
		logger.Error("error during peer handshake", zap.Error(err))
		conn.Close()
	}()

	var link multiplexer.Link
	var read int
	r := make([]byte, multiplexer.LinkSize)

	conn.SetReadDeadline(time.Now().Add(time.Second * 10))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 10))
	read, err = conn.Read(r)
	if err != nil {
		err = errors.Wrap(err, "reading handshake")
		return
	}
	if read != multiplexer.LinkSize {
		err = errors.Errorf("invalid handshake length received from peer: %d", read)
	}
	link.Unpack(r)

	logger = logger.With(zap.Uint64("peerID", link.Source))

	logger.Debug("incoming handshake with peer", zap.Uint64("peer", link.Source))

	if (link.Source == 0 || link.Destination == 0) ||
		(link.Source == link.Destination) ||
		(link.Destination != s.PeerID()) ||
		link.Source == s.PeerID() {
		err = errors.Errorf("invalid peer handshake %+v", link)
		return
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	err = s.peers.NewPeer(s.parentCtx, multiplexer.MplexProtocol, multiplexer.Config{
		Logger:    logger,
		Conn:      conn,
		Peer:      link.Source,
		Initiator: false,
		Wait:      time.Second,
	})
	if err != nil {
		err = errors.Wrap(err, "setting up peer")
	}
}

func (s *Server) handlePeerEvents() {
	for peer := range s.peers.Notify() {
		s.logger.Info("peer registered", zap.Bool("initiator", peer.Initiator()), zap.Uint64("peerID", peer.Peer()))
		// update our peer graph in a different goroutine with closure
		// as this may block
		go func(p multiplexer.Peer) {
			s.peerGraph.AddEdges(p.Peer(), s.PeerID())
		}(peer)
		// listen for request for forwarding from peers
		go func(p multiplexer.Peer) {
			for c := range p.Handle() {
				if c.Link == multiplexer.MessagingLink {
					go s.openMessaging(c.Conn, p.Peer())
					continue
				}
				if _, err := s.Forward(s.parentCtx, c.Conn, c.Link); err != nil {
					s.logger.Error("forwarding bidirectional stream", zap.Error(err), zap.Any("link", c.Link))
					c.Conn.Close()
				}
			}
			s.logger.Debug("exiting peer streams handler", zap.Uint64("peerID", p.Peer()))
		}(peer)
		// handle forward request from peers
		go peer.Start(s.parentCtx)

		// open a messaging stream
		// but only one side of the session can
		go func(p multiplexer.Peer) {
			if p.Initiator() {
				return
			}
			c, err := p.Messaging(s.parentCtx)
			if err != nil {
				s.logger.Error("opening messaging stream", zap.Error(err), zap.Uint64("Peer", p.Peer()))
				return
			}
			s.openMessaging(c, p.Peer())
		}(peer)

		// remove peer if they disconnected before gossip found out
		go func(p multiplexer.Peer) {
			<-p.NotifyClose()
			s.logger.Info("removing disconnected peer", zap.Uint64("peerID", p.Peer()))
			s.peers.Remove(p.Peer())
			s.peerGraph.RemovePeer(p.Peer())
			s.membershipCh <- struct{}{}
		}(peer)

		s.membershipCh <- struct{}{}
	}
}

func (s *Server) openMessaging(c net.Conn, peer uint64) {
	ch := s.messaging.Register(peer, c)
	s.handlePeerMessaging(ch, peer)
}
