package multiplexer

import (
	"context"
	"net"
	"time"
)

// LinkConnection contains the net.Conn associated with the Link
type LinkConnection struct {
	Conn net.Conn
	Link Link
}

// Peer defines the behavior of a multiplexer
type Peer interface {
	// Start will accept incoming connections from another Peer
	Start(context.Context)
	// Null should disconnect the incoming Peer if an incoming connection is accepted
	Null(context.Context)
	// Addr should return the Peer's net.Addr
	Addr() net.Addr
	// Initiator should return if the Peer initiated the connection
	Initiator() bool
	// Protocol returns the multiplexer protocol in used with the peer
	Protocol() Protocol
	// Peer returns the uint64 identifier of the connected Peer
	Peer() uint64
	// Ping is useful for checking latency and health
	Ping() (time.Duration, error)
	// Messaging opens a dedicated bidirectional stream to handle in-band control messages
	Messaging(context.Context) (net.Conn, error)
	// Bidirectional establishs a virtual link between the Source and Destination via this Peer
	Bidirectional(context.Context, net.Conn, Link) (<-chan error, error)
	// Direct requests a direct link to destination without automatic bidirectional handling
	Direct(context.Context, Link) (net.Conn, error)
	// Handle returns a receving channel where Bidirectional request is made from a connected peer
	Handle() <-chan LinkConnection
	// NotifyClose returns a notifying channel, unblocks when the Peer disconnects
	NotifyClose() <-chan struct{}
	// Bye closes the session and LinkConnection channel
	Bye() error
}
