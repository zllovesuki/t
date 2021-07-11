package server

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/pkg/errors"
	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server/state"
)

type Server struct {
	c             context.Context
	id            int64
	meta          Meta
	peers         *state.PeerMap
	clients       *state.PeerMap
	remoteClients *state.ClientMap
	notifier      *Notifier
	listener      net.Listener
}

func NewServer(listener net.Listener) (*Server, error) {
	rand.Seed(time.Now().UnixNano())

	self := rand.Int63()
	addr := listener.Addr().(*net.TCPAddr)

	pMap := state.NewPeerMap(self)
	cMap := state.NewPeerMap(self)
	rcMap := state.NewClientMap(self)

	s := &Server{
		meta: Meta{
			PeerID:      self,
			Multiplexer: fmt.Sprintf("%s:%d", addr.IP, addr.Port),
		},
		peers:         pMap,
		clients:       cMap,
		remoteClients: rcMap,
		notifier:      NewNotifer(),
		id:            self,
		listener:      listener,
	}

	return s, nil
}

func (s *Server) Start(ctx context.Context) {
	s.c = ctx
	go func() {
		for {
			<-time.After(time.Second * 10)
			fmt.Printf("%d\n", s.PeerID())
			s.peers.Print()
			s.clients.Print()
			s.remoteClients.Print()
		}
	}()
	for {
		conn, err := s.listener.Accept()
		fmt.Printf("new connection\n")
		if err != nil {
			fmt.Printf("error accepting peer connection: %+v\n", err)
			return
		}
		go s.handshake(ctx, conn)
	}
}

func (s *Server) PeerID() int64 {
	return s.id
}

func (s *Server) Meta() []byte {
	return s.meta.Pack()
}

func (s *Server) handshake(ctx context.Context, conn net.Conn) {
	var pair multiplexer.Pair
	r := make([]byte, 16)

	fmt.Printf("initiating handshake\n")

	if _, err := conn.Read(r); err != nil {
		fmt.Printf("failed to read handshake: %+v\n", err)
		return
	}
	pair.Unpack(r)

	if pair.Destination == 0 {
		fmt.Printf("handshake from client: %+v\n", pair)

		pair.Destination = s.PeerID()
		w := pair.Pack()
		if _, err := conn.Write(w); err != nil {
			fmt.Printf("failed to write handshake: %+v\n", err)
			return
		}

		p, err := s.clients.NewPeer(ctx, conn, pair.Source, false)
		if err != nil {
			fmt.Printf("error setting up client: %+v\n", err)
			return
		}

		update := state.ClientUpdate{
			Peer:      s.PeerID(),
			Client:    pair.Source,
			Connected: true,
		}
		s.notifier.Broadcast(update.Pack())

		go s.clientListener(ctx, p)
	} else {
		fmt.Printf("handshake from peer: %+v\n", pair)

		p, err := s.peers.NewPeer(ctx, conn, pair.Source, false)
		if err != nil {
			fmt.Printf("error setting up peer: %+v\n", err)
			return
		}
		go s.peerListeners(ctx, p)
	}
}

func (s *Server) clientListener(ctx context.Context, p *multiplexer.Peer) {
	go p.Start(ctx)
	go func() {
		peer := <-p.NotifyClose()
		fmt.Printf("removing client %+v\n", peer)
		s.clients.Remove(peer)
		update := state.ClientUpdate{
			Peer:      s.PeerID(),
			Client:    peer,
			Connected: false,
		}
		s.notifier.Broadcast(update.Pack())
	}()
}

func (s *Server) findSession(pair multiplexer.Pair) *multiplexer.Peer {
	// is the client connected locally?
	p := s.clients.Get(pair.Destination)
	// can we find a peer that has the client?
	if p == nil {
		peer := s.remoteClients.GetPeer(pair.Destination)
		p = s.peers.Get(peer)
	}
	return p
}

func (s *Server) Open(ctx context.Context, conn net.Conn, pair multiplexer.Pair) error {
	p := s.findSession(pair)
	if p == nil {
		conn.Write([]byte("HTTP/1.1 406 Not Acceptable\r\nContent-Length: 18\r\nContent-Type: text/plan\r\n\r\nendpoint not found"))
		return errors.Errorf("destination %d not found among peers", pair.Destination)
	}
	fmt.Printf("forwarding stream: %+v\n", pair)
	go p.Bidirectional(ctx, conn, pair)
	return nil
}

func (s *Server) peerListeners(ctx context.Context, p *multiplexer.Peer) {
	go p.Start(ctx)
	go func() {
		peer := <-p.NotifyClose()
		fmt.Printf("removing peer %+v\n", peer)
		s.peers.Remove(peer)
		s.remoteClients.RemovePeer(peer)
		s.notifier.Remove(peer)
	}()
	go func() {
		for c := range p.Handle(ctx) {
			if c.Pair.Destination == 0 && c.Pair.Source == 0 {
				fmt.Printf("opening notify channel with peer %+v\n", p.Peer())
				ch := s.notifier.Put(p.Peer(), c.Conn)
				go s.handleNotify(ctx, ch)
				continue
			}
			s.Open(ctx, c.Conn, c.Pair)
		}
	}()
	go func() {
		fmt.Printf("incoming notify stream %+v\n", p.Peer())
		c, err := p.OpenNotify()
		if err != nil {
			fmt.Printf("cannot open notify stream: %+v\n", err)
			return
		}
		ch := s.notifier.Put(p.Peer(), c)
		go s.handleNotify(ctx, ch)
	}()
}

func (s *Server) handleNotify(ctx context.Context, ch <-chan []byte) {
	for b := range ch {
		var x state.ClientUpdate
		x.Unpack(b)

		fmt.Printf("notify: %+v\n", x)

		switch x.Connected {
		case true:
			s.remoteClients.Put(x.Client, x.Peer)
		case false:
			s.remoteClients.RemoveClient(x.Client)
		}
	}
}

var _ memberlist.EventDelegate = &Server{}

func (s *Server) NotifyJoin(node *memberlist.Node) {
	if node.Name == fmt.Sprint(s.PeerID()) {
		fmt.Printf("connecting to ourself, returning\n")
		return
	}
	if node.Meta == nil {
		return
	}

	var m Meta
	if err := m.Unpack(node.Meta); err != nil {
		fmt.Printf("error unpacking node meta: %+v\n", err)
		return
	}

	fmt.Printf("new peer discovered: %+v\n", m)

	go s.connectPeer(s.c, m)
}

func (s *Server) NotifyLeave(node *memberlist.Node) {
	if node.Name == fmt.Sprint(s.PeerID()) {
		fmt.Printf("removing ourself, returning\n")
		return
	}

	var m Meta
	if err := m.Unpack(node.Meta); err != nil {
		fmt.Printf("error unpacking node meta: %+v\n", err)
		return
	}

	go s.removePeer(s.c, m)
}

func (s *Server) NotifyUpdate(node *memberlist.Node) {
	fmt.Printf("node update: %+v\n", node)
}

func (s *Server) connectPeer(ctx context.Context, m Meta) {
	conn, err := net.Dial("tcp", m.Multiplexer)
	if err != nil {
		fmt.Printf("error connecting to new peer: %+v\n", err)
		return
	}

	fmt.Printf("initiating handshake with peer\n")

	pair := multiplexer.Pair{
		Source:      s.PeerID(),
		Destination: m.PeerID,
	}
	buf := pair.Pack()
	conn.Write(buf)

	p, err := s.peers.NewPeer(ctx, conn, pair.Destination, true)
	if err != nil {
		fmt.Printf("error starting client session: %+v\n", err)
		return
	}

	go s.peerListeners(ctx, p)
}

func (s *Server) removePeer(ctx context.Context, m Meta) {
	p := s.peers.Get(m.PeerID)
	if p == nil {
		return
	}

	fmt.Printf("peer disconnecting: %+v\n", m)

	if err := p.Bye(); err != nil {
		fmt.Printf("error disconnecting peer: %+v\n", err)
	}
}

// var _ memberlist.Delegate = &Server{}

// func (s *Server) GetBroadcasts(overhead, limit int) [][]byte {
// 	// fmt.Printf("GetBroadcasts invoked: %d %d\n", overhead, limit)
// 	return s.broadcasts.GetBroadcasts(overhead, limit)
// }

// func (s *Server) LocalState(join bool) []byte {
// 	return nil
// }

// func (s *Server) MergeRemoteState(buf []byte, join bool) {
// }

// func (s *Server) NodeMeta(limit int) []byte {
// 	// fmt.Printf("NodeMeta invoked: %d\n", limit)
// 	return s.Meta()
// }

// func (s *Server) NotifyMsg(b []byte) {
// 	var x state.ClientUpdate
// 	x.Unpack(b)

// 	fmt.Printf("notify: %+v\n", x)

// 	switch x.Connected {
// 	case true:
// 		s.remoteClients.Put(x.Client, x.Peer)
// 	case false:
// 		s.remoteClients.RemoveClient(x.Client)
// 	}
// }
