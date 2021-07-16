package server

import (
	"math/rand"
	"net"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"

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

	var pair multiplexer.Pair
	var read int
	r := make([]byte, multiplexer.PairSize)

	conn.SetReadDeadline(time.Now().Add(time.Second * 10))
	conn.SetWriteDeadline(time.Now().Add(time.Second * 10))
	read, err = conn.Read(r)
	if err != nil {
		err = errors.Wrap(err, "reading handshake")
		return
	}
	if read != multiplexer.PairSize {
		err = errors.Errorf("invalid handshake length received from peer: %d", read)
	}
	pair.Unpack(r)

	logger = logger.With(zap.Uint64("peerID", pair.Source))

	logger.Debug("incoming handshake with peer", zap.Any("peer", pair))

	if (pair.Source == 0 || pair.Destination == 0) ||
		(pair.Source == pair.Destination) ||
		(pair.Destination != s.PeerID()) ||
		pair.Source == s.PeerID() {
		err = errors.Errorf("invalid peer handshake %+v", pair)
		return
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	err = s.peers.NewPeer(s.parentCtx, state.PeerConfig{
		Conn:      conn,
		Peer:      pair.Source,
		Initiator: false,
		Wait:      time.Second * time.Duration(rand.Intn(3)+1),
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
		go func(p *multiplexer.Peer) {
			s.peerGraph.AddEdges(p.Peer(), s.PeerID())
		}(peer)
		// listen for request for forwarding from peers
		go func(p *multiplexer.Peer) {
			for c := range p.Handle(s.parentCtx) {
				if _, err := s.Forward(s.parentCtx, c.Conn, c.Pair); err != nil {
					s.logger.Error("forwarding bidirectional stream", zap.Error(err), zap.Any("pair", c.Pair))
				}
			}
			s.logger.Debug("exiting peer streams handler", zap.Uint64("peerID", p.Peer()))
		}(peer)
		// handle forward request from peers
		go peer.Start(s.parentCtx)
	}
}