package server

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"
	"github.com/zllovesuki/t/shared"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (s *Server) clientHandshake(conn net.Conn) {
	var err error
	logger := s.logger
	defer func() {
		if err == nil {
			return
		}
		logger.Error("error during client handshake", zap.Error(err))
		conn.Close()
	}()

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

	logger = logger.With(zap.Any("remoteAddr", conn.RemoteAddr()))

	logger.Debug("incoming handshake with client", zap.Any("link", link))

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

	err = s.clients.NewPeer(s.parentCtx, state.PeerConfig{
		Conn:      conn,
		Peer:      link.Source,
		Initiator: false,
		Wait:      time.Second,
	})
	if err != nil {
		err = errors.Wrap(err, "setting up client")
	}
}

func (s *Server) handleClientEvents() {
	for peer := range s.clients.Notify() {
		s.logger.Info("client destination registered", zap.Uint64("peerID", peer.Peer()), zap.String("remote", peer.Addr().String()))
		// update our peer graph with the client as this may block with
		// many clients connecting
		go func(p multiplexer.Peer) {
			s.peerGraph.AddEdges(p.Peer(), s.PeerID())
		}(peer)
		// remove the client once they are disconnected. relying on notify
		// as clients do not partipate in gossips
		go func(p multiplexer.Peer) {
			<-p.NotifyClose()
			s.logger.Debug("removing disconnected client", zap.Uint64("peerID", p.Peer()))
			s.clients.Remove(p.Peer())
			s.peerGraph.RemovePeer(p.Peer())
		}(peer)
		// do not handle forward request from client
		go peer.Null(s.parentCtx)
	}
}
