package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/peer"
	"github.com/zllovesuki/t/shared"

	"github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (s *Server) clientQUICHandshake(sess quic.Session) {
	ctx, cancel := context.WithTimeout(s.parentCtx, time.Second*3)
	defer cancel()

	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		logger.Error("error during quic client handshake", zap.Error(err))
		sess.CloseWithError(quic.ApplicationErrorCode(0), err.Error())
	}()

	conn, err := sess.AcceptStream(ctx)
	if err != nil {
		logger.Error("error accepting quic handshake stream", zap.Error(err))
		return
	}
	defer conn.Close()

	err = s.clientNegotiation(s.logger, sess, peer.WrapQUIC(sess, conn), multiplexer.AcceptableQUICProtocols)
}

func (s *Server) clientTLSHandshake(conn net.Conn) {
	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		logger.Error("error during tls client handshake", zap.Error(err))
		conn.Close()
	}()

	err = s.clientNegotiation(s.logger, conn, conn, multiplexer.AcceptableTLSProtocols)
}

func (s *Server) clientNegotiation(logger *zap.Logger, connector interface{}, conn net.Conn, acceptableProtocols []multiplexer.Protocol) (err error) {
	var link multiplexer.Link
	var length int
	r := make([]byte, multiplexer.LinkSize)

	conn.SetReadDeadline(time.Now().Add(time.Second * 3))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 3))
	length, err = conn.Read(r)
	if err != nil {
		err = errors.Wrap(err, "reading handshake")
		return
	}
	if length != multiplexer.LinkSize {
		err = errors.Errorf("invalid handshake length received from client: %d", length)
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

	_, err = multiplexer.Get(link.Protocol)
	if err != nil {
		err = errors.Wrap(err, "negotiating protocol with client")
		return
	}

	logger = logger.With(zap.Any("remoteAddr", conn.RemoteAddr()), zap.Any("link", link))

	logger.Debug("incoming handshake with client")

	if link.Source != 0 || link.Destination != 0 {
		err = errors.Errorf("invalid client handshake %+v", link)
		return
	}

	name := shared.RandomHostname()
	link.Source = shared.PeerHash(name)
	link.Destination = s.PeerID()
	w := link.Pack()

	length, err = conn.Write(w)
	if err != nil {
		err = errors.Wrap(err, "replying handshake")
		return
	}
	if length != multiplexer.LinkSize {
		err = errors.Errorf("invalid handshake length sent to client: %d", length)
		return
	}
	err = json.NewEncoder(conn).Encode(&shared.GeneratedName{
		Hostname: fmt.Sprintf("https://%s.%s", name, s.config.Domain),
	})
	if err != nil {
		err = errors.Wrap(err, "writing generated hostname to client")
		return
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	err = s.clients.NewPeer(s.parentCtx, link.Protocol, multiplexer.Config{
		Logger:    logger,
		Conn:      connector,
		Peer:      link.Source,
		Initiator: false,
		Wait:      time.Second,
	})
	if err != nil {
		err = errors.Wrap(err, "setting up client")
	}

	return
}

func (s *Server) handleClientEvents() {
	for peer := range s.clients.Notify() {
		s.logger.Info("client destination registered", zap.Uint64("peerID", peer.Peer()), zap.String("remote", peer.Addr().String()), zap.String("protocol", peer.Protocol().String()))
		// update our peer graph with the client as this may block with
		// many clients connecting
		go func(p multiplexer.Peer) {
			s.peerGraph.AddEdges(p.Peer(), s.PeerID())
		}(peer)
		// remove the client once they are disconnected. relying on notify
		// as clients do not partipate in gossips
		go func(p multiplexer.Peer) {
			select {
			case <-s.parentCtx.Done():
			case <-p.NotifyClose():
				s.logger.Debug("removing disconnected client", zap.Uint64("peerID", p.Peer()))
				s.clients.Remove(p.Peer())
				s.peerGraph.RemovePeer(p.Peer())
			}
		}(peer)
		// do not handle forward request from client
		go peer.Null(s.parentCtx)
	}
}
