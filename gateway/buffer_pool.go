package gateway

import (
	"net/http/httputil"
	"sync"
)

const (
	bufferSize = 32 * 1024
)

type bufferPool struct {
	pool *sync.Pool
}

func newBufferPool() *bufferPool {
	return &bufferPool{
		pool: &sync.Pool{
			New: func() interface{} {
				b := make([]byte, bufferSize)
				return &b
			},
		},
	}
}

var _ httputil.BufferPool = &bufferPool{}

func (b *bufferPool) Get() []byte {
	return *b.pool.Get().(*[]byte)
}

func (b *bufferPool) Put(buf []byte) {
	b.pool.Put(&buf)
}
