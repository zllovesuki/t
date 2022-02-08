package gateway

import (
	"net/http/httputil"

	pool "github.com/libp2p/go-buffer-pool"
)

const (
	bufferSize = 8 * 1024
)

type bufferPool struct {
}

func newBufferPool() *bufferPool {
	return &bufferPool{}
}

var _ httputil.BufferPool = &bufferPool{}

func (b *bufferPool) Get() []byte {
	return pool.Get(bufferSize)
}

func (b *bufferPool) Put(buf []byte) {
	pool.Put(buf)
}
