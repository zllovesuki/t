package messaging

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
)

var (
	ErrPeerGone   = fmt.Errorf("peer was disconnected from messaging channel")
	ErrRPCTimeout = fmt.Errorf("rpc call timeout")
)

type MessageType int

const (
	MessageUnknown MessageType = iota
	MessageACMEAccountKey
	MessageClientCerts
	MessageTest

	messageRequestReply
)

const (
	messageHeaderLength = 24
	rrHeaderLength      = 17
)

type Message struct {
	Type MessageType
	From uint64
	Data []byte
}

type Channel struct {
	peers  *sync.Map
	logger *zap.Logger
	rr     *sync.Map
	req    chan Request
	self   uint64
}

type Request struct {
	Reply chan []byte
	Data  []byte
}
