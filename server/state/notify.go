package state

import (
	"net"
	"sync"

	"go.uber.org/zap"
)

type peerComm struct {
	conn net.Conn
	ch   chan ClientUpdate
}

type direction struct {
	peer uint64
	in   bool
}

type Notifier struct {
	mu     sync.RWMutex
	peers  sync.Map
	logger *zap.Logger
}

func NewNotifer(logger *zap.Logger) *Notifier {
	return &Notifier{
		logger: logger,
	}
}

func (n *Notifier) Put(peer uint64, conn net.Conn, in bool) <-chan ClientUpdate {
	n.mu.Lock()
	k := direction{
		peer: peer,
		in:   in,
	}
	p := peerComm{
		conn: conn,
		ch:   make(chan ClientUpdate),
	}
	_, loaded := n.peers.LoadOrStore(k, p)
	if loaded {
		n.mu.Unlock()
		n.logger.Warn("duplicate notifier for peer", zap.Uint64("peer", peer), zap.Bool("in", in))
		return nil
	}
	n.mu.Unlock()

	go func(peer uint64, p peerComm) {
		recv := make([]byte, ClientUpdateSize)
		for {
			l, err := p.conn.Read(recv)
			if l > 0 {
				var x ClientUpdate
				x.Unpack(recv)
				p.ch <- x
			}
			if err != nil {
				n.logger.Error("reading notifying stream", zap.Uint64("peer", peer), zap.Error(err))
				p.conn.Close()
				close(p.ch)
				break
			}
		}
	}(peer, p)

	return p.ch
}

func (n *Notifier) Remove(peer uint64) {
	n.mu.Lock()
	inc, ok := n.peers.LoadAndDelete(direction{
		peer: peer,
		in:   true,
	})
	if ok {
		comm := inc.(peerComm)
		comm.conn.Close()
	}
	out, ok := n.peers.LoadAndDelete(direction{
		peer: peer,
		in:   false,
	})
	if ok {
		comm := out.(peerComm)
		comm.conn.Close()
	}
	n.mu.Unlock()
}

func (n *Notifier) Broadcast(b []byte) {
	n.mu.RLock()
	n.peers.Range(func(peer, comm interface{}) bool {
		k := peer.(direction)
		if k.in {
			return true
		}
		_, err := comm.(peerComm).conn.Write(b)
		if err != nil {
			n.logger.Error("notifying peer", zap.Uint64("peer", k.peer), zap.Error(err))
		}
		return true
	})
	n.mu.RUnlock()
}
