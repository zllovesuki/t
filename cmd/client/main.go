package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/shared"

	"github.com/sethvargo/go-diceware/diceware"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

var (
	peer    = flag.String("peer", "127.0.0.1:11111", "server")
	forward = flag.String("forward", "127.0.0.1:3000", "where to forward")
)

func getRandomName() string {
	g, err := diceware.Generate(5)
	if err != nil {
		return ""
	}
	return strings.Join(g, "-")
}

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector, err := net.Dial("tcp", *peer)
	if err != nil {
		fmt.Printf("error listening: %+v\n", err)
		return
	}

	name := getRandomName()
	peerID := shared.PeerHash(name)

	pair := multiplexer.Pair{
		Source: peerID,
	}
	buf := pair.Pack()
	connector.Write(buf)

	connector.Read(buf)
	pair.Unpack(buf)

	p, err := multiplexer.NewPeer(multiplexer.PeerConfig{
		Conn:      connector,
		Initiator: true,
		Peer:      pair.Destination,
	})
	if err != nil {
		fmt.Printf("error setting up peer: %+v\n", err)
		return
	}

	rtt, err := p.Ping()
	if err != nil {
		fmt.Printf("error checking connection: %+v\n", err)
		return
	}

	fmt.Printf("peering: %+v\n", pair)
	fmt.Printf("rtt time with server %s\n", rtt)
	fmt.Printf("your hostname: %+v\n", name)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go p.Start(ctx)
	go func() {
		<-p.NotifyClose()
		fmt.Printf("peer %d disconnected\n", p.Peer())
		sigs <- syscall.SIGTERM
	}()
	go func() {
		for c := range p.Handle(ctx) {
			o, err := net.Dial("tcp", *forward)
			if err != nil {
				fmt.Printf("error connecting to %s: %+v\n", *forward, err)
				continue
			}
			errCh := multiplexer.Connect(ctx, o, c.Conn)
			go func() {
				for e := range errCh {
					if multiplexer.IsTimeout(e) {
						continue
					}
					fmt.Printf("%+v\n", e)
				}
			}()
		}
	}()

	<-sigs

	if err := p.Bye(); err != nil {
		fmt.Printf("cannot say Bye: %+v\n", err)
	}
}
