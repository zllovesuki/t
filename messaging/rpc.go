package messaging

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"runtime"
	"time"

	"go.uber.org/zap"
)

var (
	ErrRPCTimeout = fmt.Errorf("rpc call timeout")
)

const (
	rrHeaderLength = 17
)

type requestReply struct {
	request bool
	key     uint64
	data    []byte
}

func (r *requestReply) Pack() []byte {
	dl := len(r.data)
	buf := make([]byte, rrHeaderLength+dl)
	binary.BigEndian.PutUint64(buf[0:8], uint64(dl))
	binary.BigEndian.PutUint64(buf[8:16], r.key)
	if r.request {
		buf[16] = 1
	}
	copy(buf[rrHeaderLength:], r.data)
	return buf
}

func (r *requestReply) Unpack(b []byte) {
	dl := binary.BigEndian.Uint64(b[0:8])
	r.key = binary.BigEndian.Uint64(b[8:16])
	r.data = make([]byte, dl)
	if b[16] == 1 {
		r.request = true
	}
	copy(r.data, b[rrHeaderLength:])
}

type Request struct {
	Reply chan []byte
	Data  []byte
}

func (c *Channel) HandleRequest() <-chan Request {
	return c.req
}

func (c *Channel) Call(peer uint64, b []byte) (rep []byte, err error) {
	defer func() {
		if e := recover(); e != nil {
			rep = nil
			err = ErrPeerGone
			c.logger.Error("rpc call request panic", zap.Any("error", e))
		}
	}()

	repCh := make(chan []byte)
	var key uint64
	for {
		key = rand.Uint64()
		_, ok := c.rr.LoadOrStore(key, repCh)
		if !ok {
			break
		}
		runtime.Gosched()
	}
	defer c.rr.Delete(key)

	r := requestReply{
		request: true,
		key:     key,
		data:    b,
	}
	m := Message{
		From: c.self,
		Type: messageRequestReply,
		Data: r.Pack(),
	}
	buf := m.Pack()
	p, ok := c.peers.Load(peer)
	if !ok {
		return nil, ErrPeerGone
	}
	// TODO(zllovesuki): potential race here
	conn := p.(*messageChannel).conn
	w, err := conn.Write(buf)
	if err != nil {
		return nil, err
	}
	if w != len(buf) {
		return nil, err
	}
	select {
	case <-time.After(time.Second * 10):
		return nil, ErrRPCTimeout
	case rep := <-repCh:
		return rep, nil
	}
}

func (c *Channel) handleRPC(sender uint64, r requestReply) {
	defer func() {
		if e := recover(); e != nil {
			c.logger.Error("rpc call reply panic", zap.Any("error", e))
		}
	}()

	switch r.request {
	case true:
		repCh := make(chan []byte)
		x := Request{
			Data:  r.data,
			Reply: repCh,
		}
		c.req <- x
		select {
		case <-time.After(time.Second * 10):
			return
		case r.data = <-repCh:
			r.request = false
		}
		// TODO(zllovesuki): potential race here
		p, ok := c.peers.Load(sender)
		if !ok {
			return
		}
		conn := p.(*messageChannel).conn
		reply := Message{
			From: c.self,
			Type: messageRequestReply,
			Data: r.Pack(),
		}
		buf := reply.Pack()
		w, err := conn.Write(buf)
		if err != nil {
			c.logger.Error("replying to caller", zap.Error(err))
			return
		}
		if w != len(buf) {
			c.logger.Error("mismatched length")
			return
		}
	case false:
		x, ok := c.rr.LoadAndDelete(r.key)
		if !ok {
			c.logger.Error("response channel gone")
			return
		}
		x.(chan []byte) <- r.data
	}
}
