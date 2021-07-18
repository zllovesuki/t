package messaging

import (
	"encoding/binary"
	"math/rand"
	"runtime"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
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

func (c *Channel) HandleRequest() <-chan Request {
	return c.req
}

func (c *Channel) Call(peer uint64, b []byte) (rep []byte, err error) {
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
	err = c.write(peer, m)
	if err != nil {
		err = errors.Wrap(err, "sending request to peer")
		return
	}
	select {
	case <-time.After(time.Second * 10):
		return nil, ErrRPCTimeout
	case rep := <-repCh:
		return rep, nil
	}
}

func (c *Channel) handleRPC(sender uint64, r requestReply) {
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
		reply := Message{
			From: c.self,
			Type: messageRequestReply,
			Data: r.Pack(),
		}
		if err := c.write(sender, reply); err != nil {
			c.logger.Error("rpc: enqueuing outbound response to peer", zap.Uint64("sender", sender), zap.Error(err))
		}
	case false:
		x, ok := c.rr.LoadAndDelete(r.key)
		if !ok {
			c.logger.Error("rpc: response channel gone", zap.Uint64("sender", sender))
			return
		}
		x.(chan []byte) <- r.data
	}
}
