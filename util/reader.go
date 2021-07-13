package util

import (
	"context"
	"io"
)

type readerCtx struct {
	c context.Context
	r io.Reader
}

func (r *readerCtx) Read(p []byte) (n int, err error) {
	if err := r.c.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// NewCtxReader will wrap an io.Reader and return a context-aware reader
// to allow for context cancellation
func NewCtxReader(c context.Context, r io.Reader) io.Reader {
	return &readerCtx{
		c: c,
		r: r,
	}
}
