package multiplexer

import (
	"context"
	"sync/atomic"
)

// Channel is a helper struct to coordinate the sending and closing
// of a channel between different goroutines to avoid data race
type Channel struct {
	_        uint64 // for sync/atomic
	outgoing chan LinkConnection
	incoming chan LinkConnection
	closer   chan struct{}
	closed   *int32
	chClosed *int32
	started  *int32
}

func NewChannel() *Channel {
	return &Channel{
		outgoing: make(chan LinkConnection, 32),
		incoming: make(chan LinkConnection),
		closer:   make(chan struct{}),
		closed:   new(int32),
		chClosed: new(int32),
		started:  new(int32),
	}
}

func (c *Channel) Put(l LinkConnection) {
	c.incoming <- l
}

func (c *Channel) Incoming() <-chan LinkConnection {
	return c.outgoing
}

func (c *Channel) Close() bool {
	if atomic.CompareAndSwapInt32(c.closed, 0, 1) {
		close(c.closer)
		return true
	}
	return false
}

func (c *Channel) closeChannel() {
	if atomic.CompareAndSwapInt32(c.chClosed, 0, 1) {
		close(c.outgoing)
	}
}

func (c *Channel) Closed() <-chan struct{} {
	return c.closer
}

func (c *Channel) Run(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(c.started, 0, 1) {
		return
	}
	// we can only close the outgoing channel here to avoid data race
	for {
		select {
		case <-ctx.Done():
			c.closeChannel()
			return
		case <-c.closer:
			c.closeChannel()
			return
		case x := <-c.incoming:
			c.outgoing <- x
		}
	}
}
