package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zllovesuki/t/multiplexer"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

var (
	peer    = flag.String("peer", "127.0.0.1:11111", "server")
	peerID  = flag.Int64("peerID", rand.Int63(), "peer id of the client")
	forward = flag.String("forward", "127.0.0.1:3000", "where to forward")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector, err := net.Dial("tcp", *peer)
	if err != nil {
		fmt.Printf("error listening: %+v\n", err)
		return
	}

	pair := multiplexer.Pair{
		Source: *peerID,
	}
	buf := pair.Pack()
	connector.Write(buf)

	connector.Read(buf)
	pair.Unpack(buf)

	fmt.Printf("pair: %+v\n", pair)

	p, err := multiplexer.NewPeer(multiplexer.PeerConfig{
		Conn:      connector,
		Initiator: true,
		Peer:      pair.Destination,
	})
	if err != nil {
		fmt.Printf("error setting up peer: %+v\n", err)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go p.Start(ctx)
	go func() {
		<-p.NotifyClose()
		sigs <- syscall.SIGTERM
	}()
	go func() {
		for c := range p.Handle(ctx) {
			o, err := net.Dial("tcp", *forward)
			if err != nil {
				fmt.Printf("error connecting to %s: %+v\n", *forward, err)
				return
			}
			fmt.Printf("%+v\n", o)
			go multiplexer.Connect(ctx, o, c.Conn)
		}
	}()

	<-sigs

	if err := p.Bye(); err != nil {
		fmt.Printf("cannot say Bye: %+v\n", err)
	}
}
