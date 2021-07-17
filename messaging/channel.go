package messaging

import (
	"net"
	"sync"

	"go.uber.org/zap"
)

type messageChannel struct {
	conn net.Conn
	ch   chan Message
}

type Channel struct {
	peers  *sync.Map
	logger *zap.Logger
	rr     *sync.Map
	req    chan Request
	self   uint64
}

func New(logger *zap.Logger, self uint64) *Channel {
	return &Channel{
		peers:  &sync.Map{},
		logger: logger,
		rr:     &sync.Map{},
		req:    make(chan Request),
		self:   self,
	}
}

func (c *Channel) Register(peer uint64, conn net.Conn) <-chan Message {
	r := &messageChannel{
		conn: conn,
		ch:   make(chan Message),
	}
	_, loaded := c.peers.LoadOrStore(peer, r)
	if loaded {
		panic("duplicated messaging stream when it should not be possible")
	}

	c.logger.Info("messaging stream established", zap.Uint64("peer", peer))

	go c.reader(peer, r)

	return r.ch
}

func (c *Channel) reader(peer uint64, comm *messageChannel) {
	for {
		var m Message
		err := m.ReadFrom(comm.conn)
		if err != nil {
			c.logger.Error("reading message from peer", zap.Error(err))
			c.forget(peer)
			return
		}
		switch m.Type {
		case messageRequestReply:
			var r requestReply
			r.Unpack(m.Data)
			go c.handleRPC(m.From, r)
		default:
			comm.ch <- m
		}
	}
}

func (c *Channel) forget(peer uint64) {
	comm, ok := c.peers.LoadAndDelete(peer)
	if ok {
		comm.(*messageChannel).conn.Close()
		close(comm.(*messageChannel).ch)
	}
}

func (c *Channel) Announce(t MessageType, b []byte) {
	m := Message{
		Type: t,
		From: c.self,
		Data: b,
	}
	d := m.Pack()
	c.peers.Range(func(key, value interface{}) bool {
		if value == nil {
			return true
		}
		go func(peer uint64, conn net.Conn) {
			w, err := conn.Write(d)
			if err != nil {
				c.logger.Error("annoucing to peer", zap.Error(err), zap.Uint64("peer", peer))
			}
			if w != len(d) {
				c.logger.Error("mismatched message length", zap.Int("expected", len(d)), zap.Int("actual", w), zap.Uint64("peer", peer))
			}
		}(key.(uint64), value.(*messageChannel).conn)
		return true
	})
}
