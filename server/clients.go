package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/alpn"
	"github.com/zllovesuki/t/multiplexer/protocol"
	"github.com/zllovesuki/t/mux"
	"github.com/zllovesuki/t/shared"

	"github.com/lucas-clemente/quic-go"
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

	err = s.clientNegotiation(s.logger, sess, mux.WrapQUIC(sess, conn), protocol.QUICProtos)
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

	err = s.clientNegotiation(s.logger, conn, conn, protocol.TLSProtos)
}

func (s *Server) clientNegotiation(logger *zap.Logger, connector interface{}, conn net.Conn, acceptableProtocols []protocol.Protocol) (err error) {
	var link multiplexer.Link
	r := make([]byte, multiplexer.LinkSize)

	conn.SetReadDeadline(time.Now().Add(time.Second * 3))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 3))

	_, err = conn.Read(r)
	if err != nil {
		err = fmt.Errorf("reading handshake: %w", err)
		return
	}

	err = link.UnmarshalBinary(r)
	if err != nil {
		err = fmt.Errorf("unmarshal link: %w", err)
		return
	}

	if link.ALPN != alpn.Multiplexer {
		err = errors.New("invalid ALPN received from client")
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
		err = fmt.Errorf("unacceptable protocol: %d", link.Protocol)
		return
	}

	_, err = multiplexer.New(link.Protocol)
	if err != nil {
		err = fmt.Errorf("negotiating protocol with client: %w", err)
		return
	}

	logger = logger.With(zap.String("remoteAddr", conn.RemoteAddr().String()), zap.Object("link", link))

	logger.Debug("incoming handshake with client")

	if link.Source != 0 || link.Destination != 0 {
		err = fmt.Errorf("invalid client handshake %+v", link)
		return
	}

	name := shared.RandomHostname()
	link.Source = shared.PeerHash(name)
	link.Destination = s.PeerID()

	var w []byte
	w, err = link.MarshalBinary()
	if err != nil {
		err = fmt.Errorf("marshal link as binary: %w", err)
		return
	}

	var length int
	length, err = conn.Write(w)
	if err != nil {
		err = fmt.Errorf("replying handshake: %w", err)
		return
	}
	if length != multiplexer.LinkSize {
		err = fmt.Errorf("invalid handshake length sent to client: %d", length)
		return
	}

	err = json.NewEncoder(conn).Encode(&shared.GeneratedName{
		Hostname: fmt.Sprintf("https://%s.%s", name, s.config.Domain),
	})
	if err != nil {
		err = fmt.Errorf("writing generated hostname to client: %w", err)
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
		err = fmt.Errorf("setting up client: %w", err)
	}

	return
}

func (s *Server) handleClientEvents() {
	for peer := range s.clients.Notify() {
		s.logger.Info("client destination registered", zap.Uint64("peer", peer.Peer()), zap.String("remote", peer.Addr().String()), zap.String("protocol", peer.Protocol().String()))
		// remove the client once they are disconnected. relying on notify
		// as clients do not partipate in gossips
		go func(p multiplexer.Peer) {
			s.clientEvtCh <- struct{}{}
			select {
			case <-s.parentCtx.Done():
			case <-p.NotifyClose():
				s.logger.Debug("removing disconnected client", zap.Uint64("peer", p.Peer()))
				s.clients.Remove(p.Peer())
				s.clientEvtCh <- struct{}{}
			}
		}(peer)
		// do not handle forward request from client
		go peer.Null(s.parentCtx)
	}
}
