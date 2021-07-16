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
	Timeout time.Duration
}

func (i *IdleTimeoutConn) Read(b []byte) (int, error) {
	i.Conn.SetReadDeadline(time.Now().Add(i.Timeout))
	return i.Conn.Read(b)
}

func (i *IdleTimeoutConn) Write(b []byte) (int, error) {
	i.Conn.SetWriteDeadline(time.Now().Add(i.Timeout))
	return i.Conn.Write(b)
}

func (i *IdleTimeoutConn) Close() error {
	return i.Conn.Close()
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

	// asymmetric timeout so upstream can get a timeout before downstream closes
	tDst := &IdleTimeoutConn{
		Conn:    dst,
		Timeout: time.Second * 15,
	}
	tSrc := &IdleTimeoutConn{
		Conn:    src,
		Timeout: time.Second * 10,
	}

	wg.Add(2)
	go pipe(ctx, &wg, err, tSrc, tDst)
	go pipe(ctx, &wg, err, tDst, tSrc)
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
