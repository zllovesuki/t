package multiplexer

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/hashicorp/yamux"
	"github.com/pkg/errors"
)

func Connect(ctx context.Context, dst, src net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go pipe(ctx, &wg, src, dst)
	go pipe(ctx, &wg, dst, src)

	wg.Wait()
}

func pipe(ctx context.Context, wg *sync.WaitGroup, dst, src net.Conn) {
	defer wg.Done()
	select {
	case <-ctx.Done():
		fmt.Printf("context cancelled: %+v\n", ctx.Err())
		return
	default:
		if _, err := io.Copy(dst, src); err != nil {
			if errors.Is(err, yamux.ErrStreamClosed) {
				return
			}
			fmt.Printf("error piping: %+v\n", err)
			return
		}
	}
}
