package multiplexer

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/zllovesuki/t/util"

	"github.com/hashicorp/yamux"
	"github.com/pkg/errors"
)

type IdleTimeoutConn struct {
	net.Conn
	ReadTimeout time.Duration
}

var _ net.Conn = &IdleTimeoutConn{}

func (i *IdleTimeoutConn) Read(b []byte) (int, error) {
	err := i.Conn.SetReadDeadline(time.Now().Add(i.ReadTimeout))
	if err != nil {
		return 0, err
	}
	return i.Conn.Read(b)
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

	tSrc := &IdleTimeoutConn{
		Conn:        src,
		ReadTimeout: time.Second * 60,
	}

	wg.Add(2)
	go pipe(ctx, &wg, err, dst, tSrc)
	go pipe(ctx, &wg, err, src, dst)
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
	if _, err := io.Copy(dst, util.NewCtxReader(ctx, src)); err != nil {
		errChan <- err
		return
	}
}
