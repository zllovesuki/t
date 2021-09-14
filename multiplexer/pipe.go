package multiplexer

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/getlantern/idletiming"
	"github.com/zllovesuki/t/shared"
	"github.com/zllovesuki/t/util"

	"github.com/libp2p/go-yamux/v2"
	"github.com/pkg/errors"
)

const (
	bufferSize = 32 * 1024
)

var copyBufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, bufferSize)
		return &b
	},
}

func IsTimeout(err error) bool {
	t := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, yamux.ErrTimeout)
	if t {
		return t
	}
	if e, ok := err.(net.Error); ok {
		return e.Timeout()
	}
	return false
}

func Connect(ctx context.Context, dst, src net.Conn) <-chan error {
	var wg sync.WaitGroup
	err := make(chan error, 2)

	s := idletiming.Conn(src, shared.ConnIdleTimeout, func() {
		dst.Close()
	})

	wg.Add(2)
	go pipe(ctx, &wg, err, dst, s)
	go pipe(ctx, &wg, err, s, dst)
	go func() {
		wg.Wait()
		close(err)
		dst.Close()
		src.Close()
	}()

	return err
}

func pipe(ctx context.Context, wg *sync.WaitGroup, errChan chan<- error, dst, src net.Conn) {
	defer wg.Done()
	pBuf := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(pBuf)
	if _, err := io.CopyBuffer(dst, util.NewCtxReader(ctx, src), *pBuf); err != nil {
		errChan <- err
		return
	}
}
