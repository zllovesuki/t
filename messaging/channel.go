package messaging

import (
	"io"
	"net"
	"sync"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type messageChannel struct {
	conn net.Conn
	in   chan Message
	out  chan Message
}

func New(logger *zap.Logger, self uint64) *Channel {
	return &Channel{
		peers:  &sync.Map{},
		logger: logger,
		rr:     &sync.Map{},
		req:    make(chan Request, 16),
		self:   self,
	}
}

func (c *Channel) Register(peer uint64, conn net.Conn) <-chan Message {
	r := &messageChannel{
		conn: conn,
		in:   make(chan Message, 16),
		out:  make(chan Message, 16),
	}
	_, loaded := c.peers.LoadOrStore(peer, r)
	if loaded {
		panic("duplicated messaging stream when it should not be possible")
	}

	c.logger.Debug("messaging stream established", zap.Uint64("peer", peer))

	var wg sync.WaitGroup
	wg.Add(2)
	go c.writer(&wg, peer, r)
	go c.reader(&wg, peer, r)
	go func() {
		wg.Wait()
		c.logger.Debug("closing messaging stream with peer", zap.Uint64("peer", peer))
		c.forget(peer)
	}()

	return r.in
}

func (c *Channel) writer(wg *sync.WaitGroup, peer uint64, comm *messageChannel) {
	defer wg.Done()
	for m := range comm.out {
		b := m.Pack()
		w, err := comm.conn.Write(b)
		if err != nil {
			c.logger.Error("writing message to peer", zap.Error(err))
			return
		}
		if w != len(b) {
			c.logger.Error("mismatched message length", zap.Int("expected", len(b)), zap.Int("actual", w), zap.Uint64("peer", peer))
			return
		}
	}
}

func (c *Channel) reader(wg *sync.WaitGroup, peer uint64, comm *messageChannel) {
	defer wg.Done()
	for {
		var m Message
		err := m.ReadFrom(comm.conn)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.logger.Error("reading message from peer", zap.Error(err))
			}
			c.forget(peer)
			return
		}
		switch m.Type {
		case messageRequestReply:
			var r requestReply
			r.Unpack(m.Data)
			go c.handleRPC(m.From, r)
		default:
			comm.in <- m
		}
	}
}

func (c *Channel) forget(peer uint64) {
	comm, ok := c.peers.LoadAndDelete(peer)
	if ok {
		comm.(*messageChannel).conn.Close()
		close(comm.(*messageChannel).in)
		close(comm.(*messageChannel).out)
	}
}

func (c *Channel) write(peer uint64, m Message) (err error) {
	defer func() {
		if e := recover(); e != nil {
			c.logger.Error("panic: peer channel closed", zap.Uint64("peer", peer))
			err = ErrPeerGone
		}
	}()
	var comm *messageChannel
	x, ok := c.peers.Load(peer)
	if !ok {
		c.logger.Error("peer disconnected while attempting to write", zap.Uint64("peer", peer))
		return ErrPeerGone
	}
	comm = x.(*messageChannel)
	comm.out <- m
	return nil
}

func (c *Channel) Announce(t MessageType, b []byte) {
	m := Message{
		Type: t,
		From: c.self,
		Data: b,
	}
	c.peers.Range(func(key, value interface{}) bool {
		if value == nil {
			return true
		}
		go c.write(key.(uint64), m)
		return true
	})
}
