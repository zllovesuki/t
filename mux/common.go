package mux

import (
	"fmt"
	"io"

	"github.com/zllovesuki/t/multiplexer"
)

func directHandshake(link multiplexer.Link, w io.Writer) error {
	buf, err := link.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal link as binary: %w", err)
	}

	written, err := w.Write(buf)
	if err != nil {
		return fmt.Errorf("writing stream handshake: %w", err)
	}
	if written != multiplexer.LinkSize {
		return fmt.Errorf("invalid handshake length: %d", written)
	}

	return nil
}

func messagingHandshake(w io.Writer) error {
	link := multiplexer.MessagingLink
	return directHandshake(link, w)
}
