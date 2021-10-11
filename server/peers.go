package server

import (
	"context"
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/alpn"
	"github.com/zllovesuki/t/multiplexer/protocol"
	"github.com/zllovesuki/t/mux"
	"github.com/zllovesuki/t/state"

	"github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (s *Server) peerQUICHandshake(sess quic.Session) {
	ctx, cancel := context.WithTimeout(s.parentCtx, time.Second*3)
	defer cancel()

	var err error
	var conn quic.Stream
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		sess.CloseWithError(quic.ApplicationErrorCode(0), err.Error())
	}()

	conn, err = sess.AcceptStream(ctx)
	if err != nil {
		logger.Error("error accepting quic handshake stream", zap.Error(err))
		return
	}
	defer conn.Close()

	err = s.peerNegotiation(s.logger, sess, mux.WrapQUIC(sess, conn), protocol.QUICProtos)
}

func (s *Server) peerTLSHandshake(conn net.Conn) {
	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		conn.Close()
	}()

	err = s.peerNegotiation(logger, conn, conn, protocol.TLSProtos)
}

func (s *Server) peerNegotiation(logger *zap.Logger, connector interface{}, conn net.Conn, acceptableProtocols []protocol.Protocol) (err error) {
	defer func() {
		if errors.Is(err, state.ErrSessionAlreadyEstablished) {
			logger.Info("reusing established session")
			return
		}
		logger.Debug("error during peer handshake", zap.Error(err))
	}()

	var link multiplexer.Link
	r := make([]byte, multiplexer.LinkSize)

	conn.SetReadDeadline(time.Now().Add(time.Second * 10))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 10))
	_, err = conn.Read(r)
	if err != nil {
		err = errors.Wrap(err, "reading handshake")
		return
	}

	err = link.UnmarshalBinary(r)
	if err != nil {
		err = errors.Wrap(err, "unmarshal link")
		return
	}

	if link.ALPN != alpn.Multiplexer {
		err = errors.New("invalid ALPN received from peer")
		return
	}

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

	logger = logger.With(zap.Uint64("peerID", link.Source), zap.Object("link", link))

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
		s.logger.Info("peer registered", zap.Bool("initiator", peer.Initiator()), zap.Uint64("peer", peer.Peer()), zap.String("protocol", peer.Protocol().String()))
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
				s.logger.Debug("forwarding bidirectional stream", zap.Object("link", c.Link))
				if _, err := s.Forward(s.parentCtx, c.Conn, c.Link); err != nil {
					s.logger.Error("forwarding bidirectional stream", zap.Error(err), zap.Object("link", c.Link))
					c.Conn.Close()
				}
			}
			s.logger.Debug("exiting peer streams handler", zap.Uint64("peer", p.Peer()))
		}(peer)
		// handle forward request from peers
		go peer.Start(s.parentCtx)

		// open a messaging stream
		// but only one side of the session can
		go func(p multiplexer.Peer) {
			if p.Initiator() {
				s.logger.Debug("skip openning message stream as initiator", zap.Uint64("peer", p.Peer()))
				return
			}
			c, err := p.Messaging(s.parentCtx)
			if err != nil {
				s.logger.Error("opening messaging stream", zap.Error(err), zap.Uint64("peer", p.Peer()))
				return
			}
			go s.openMessaging(c, p.Peer())
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
	s.logger.Debug("opening message stream with peer", zap.Uint64("peer", peer))
	ch := s.messaging.Register(peer, c)
	s.handlePeerMessaging(ch, peer)
}

func (s *Server) removePeer(peer uint64) {
	s.logger.Info("removing disconnected peer", zap.Uint64("peer", peer))
	s.peers.Remove(peer)
	s.peerGraph.RemovePeer(peer)
	s.membershipCh <- struct{}{}
}
