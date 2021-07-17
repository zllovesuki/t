package messaging

import "fmt"

var (
	ErrPeerGone = fmt.Errorf("peer was disconnected from messaging channel")
)

type MessageType int

const (
	MessageUnknown MessageType = iota
	MessageACMEAccountKey
	MessageClientCerts

	messageRequestReply
)

const (
	messageHeaderLength = 24
)
