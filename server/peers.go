package server

import (
	"context"
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/peer"
	"github.com/zllovesuki/t/state"

	"github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (s *Server) peerQUICHandshake(sess quic.Session) {
	ctx, cancel := context.WithTimeout(s.parentCtx, time.Second*3)
	defer cancel()

	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		logger.Error("error during quic peer handshake", zap.Error(err))
		sess.CloseWithError(quic.ApplicationErrorCode(0), err.Error())
	}()

	conn, err := sess.AcceptStream(ctx)
	if err != nil {
		logger.Error("error accepting quic handshake stream", zap.Error(err))
		return
	}
	defer conn.Close()

	err = s.peerNegotiation(s.logger, sess, peer.WrapQUIC(sess, conn), multiplexer.AcceptableQUICProtocols)
}

func (s *Server) peerTLSHandshake(conn net.Conn) {
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

	err = s.peerNegotiation(logger, conn, conn, multiplexer.AcceptableTLSProtocols)
}

func (s *Server) peerNegotiation(logger *zap.Logger, connector interface{}, conn net.Conn, acceptableProtocols []multiplexer.Protocol) (err error) {
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
		return
	}
	link.Unpack(r)

	validProtocol := false
	for _, p := range acceptableProtocols {
		if p == link.Protocol {
			validProtocol = true
			break
		}
	}
	if !validProtocol {
		err = errors.Errorf("unacceptable protocol: %d", link.Protocol)
		return
	}

	_, err = multiplexer.New(link.Protocol)
	if err != nil {
		err = errors.Wrap(err, "negotiating protocol with peer")
		return
	}

	logger = logger.With(zap.Uint64("peerID", link.Source), zap.Any("link", link))

	logger.Debug("incoming handshake with peer")

	if (link.Source == 0 || link.Destination == 0) ||
		(link.Source == link.Destination) ||
		(link.Destination != s.PeerID()) ||
		link.Source == s.PeerID() {
		err = errors.Errorf("invalid peer handshake %+v", link)
		return
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	err = s.peers.NewPeer(s.parentCtx, link.Protocol, multiplexer.Config{
		Logger:    logger,
		Conn:      connector,
		Peer:      link.Source,
		Initiator: false,
		Wait:      time.Second,
	})
	if err != nil {
		err = errors.Wrap(err, "setting up peer")
	}

	return
}

func (s *Server) handlePeerEvents() {
	for peer := range s.peers.Notify() {
		s.logger.Info("peer registered", zap.Bool("initiator", peer.Initiator()), zap.Uint64("peerID", peer.Peer()), zap.String("protocol", peer.Protocol().String()))
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
			select {
			case <-s.parentCtx.Done():
			case <-p.NotifyClose():
				s.removePeer(p.Peer())
			}
		}(peer)

		s.membershipCh <- struct{}{}
	}
}

func (s *Server) openMessaging(c net.Conn, peer uint64) {
	ch := s.messaging.Register(peer, c)
	s.handlePeerMessaging(ch, peer)
}

func (s *Server) removePeer(peer uint64) {
	if s.peers.Get(peer) == nil {
		s.logger.Info("removing non-existent peer", zap.Uint64("peerID", peer))
		return
	}
	s.logger.Info("removing disconnected peer", zap.Uint64("peerID", peer))
	s.peers.Remove(peer)
	s.peerGraph.RemovePeer(peer)
	s.membershipCh <- struct{}{}
}
