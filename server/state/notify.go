package state

import (
	"net"
	"sync"
)

type Notifier struct {
	mu       sync.Mutex
	peers    map[uint64]net.Conn
	channels map[uint64]chan ClientUpdate
}

func NewNotifer() *Notifier {
	return &Notifier{
		peers:    make(map[uint64]net.Conn),
		channels: make(map[uint64]chan ClientUpdate),
	}
}

func (n *Notifier) Put(peer uint64, conn net.Conn) <-chan ClientUpdate {
	n.mu.Lock()
	defer n.mu.Unlock()

	ch := make(chan ClientUpdate)
	n.peers[peer] = conn
	n.channels[peer] = ch

	go func(conn net.Conn) {
		recv := make([]byte, ClientUpdateSize)
		for {
			n, err := conn.Read(recv)
			if n > 0 {
				var x ClientUpdate
				x.Unpack(recv)
				ch <- x
			}
			if err != nil {
				// fmt.Printf("error in notify stream: %+v\n", err)
				close(ch)
				break
			}
		}
	}(conn)

	return ch
}

func (n *Notifier) Remove(peer uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()

	delete(n.peers, peer)
	delete(n.channels, peer)
}

func (n *Notifier) Broadcast(b []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()

	dead := []uint64{}
	for p, c := range n.peers {
		_, err := c.Write(b)
		if err != nil {
			dead = append(dead, p)
			// fmt.Printf("error notifing peer %d: %+v\n", p, err)
		}
	}

	for _, d := range dead {
		n.peers[d].Close()
		delete(n.peers, d)
		delete(n.channels, d)
	}
}
