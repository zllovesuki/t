package server

import (
	"fmt"
	"net"
	"sync"
)

type Notifier struct {
	mu       sync.Mutex
	peers    map[int64]net.Conn
	channels map[int64]chan []byte
}

func NewNotifer() *Notifier {
	return &Notifier{
		peers:    make(map[int64]net.Conn),
		channels: make(map[int64]chan []byte),
	}
}

func (n *Notifier) Put(peer int64, conn net.Conn) <-chan []byte {
	n.mu.Lock()
	defer n.mu.Unlock()

	ch := make(chan []byte)
	n.peers[peer] = conn
	n.channels[peer] = ch

	go func() {
		recv := make([]byte, 32)
		dup := make([]byte, 32)
		for {
			n, err := conn.Read(recv)
			if n > 0 {
				copy(dup, recv)
				ch <- dup
			}
			if err != nil {
				fmt.Printf("error in notify stream: %+v\n", err)
				close(ch)
				break
			}
		}
	}()

	return ch
}

func (n *Notifier) Remove(peer int64) {
	n.mu.Lock()
	defer n.mu.Unlock()

	delete(n.peers, peer)
	delete(n.channels, peer)
}

func (n *Notifier) Broadcast(b []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for p, c := range n.peers {
		_, err := c.Write(b)
		if err != nil {
			fmt.Printf("error notifing peer %d: %+v\n", p, err)
		}
	}
}
