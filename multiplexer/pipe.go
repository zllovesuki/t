package multiplexer

import (
	"context"
	"io"
	"net"
	"sync"

	"log"

	"github.com/zllovesuki/t/profiler"
	"github.com/zllovesuki/t/util"

	"github.com/libp2p/go-yamux/v3"
	"github.com/pkg/errors"
)

const (
	bufferSize = 16 * 1024
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

	wg.Add(2)
	go pipe(ctx, &wg, err, "<", dst, src)
	go pipe(ctx, &wg, err, ">", src, dst)
	go func() {
		wg.Wait()
		log.Printf("pipe closed: %s <-> %s", src.LocalAddr(), dst.RemoteAddr())
		profiler.ConnectionStats.WithLabelValues("closed", "bidirectional").Inc()
		close(err)
	}()

	return err
}

func pipe(ctx context.Context, wg *sync.WaitGroup, errChan chan<- error, dir string, dst, src net.Conn) {
	defer wg.Done()
	pBuf := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(pBuf)

	// io.Copy yeet the EOF from reader and turns it into nil.
	// therefore, in the previous iteration, one side of pipe
	// never returns and therefore keeping the pipe from closing.
	// Here we will forcibly close both ends as soon as io.Copy returns.
	n, err := io.CopyBuffer(dst, util.NewCtxReader(ctx, src), *pBuf)
	log.Printf("CopyBuffer: (%s <-> %s) %s", src.LocalAddr(), dst.RemoteAddr(), err)

	src.Close()
	dst.Close()

	switch dir {
	case "<":
		profiler.ConnectionStats.WithLabelValues("src_to_dst", "bidirectional").Add(float64(n))
	case ">":
		profiler.ConnectionStats.WithLabelValues("dst_to_src", "bidirectional").Add(float64(n))
	}

	if err != nil {
		errChan <- err
		return
	}
}
