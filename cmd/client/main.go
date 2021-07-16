package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/shared"
	"go.uber.org/zap"

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

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector, err := tls.Dial("tcp", *peer, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		logger.Error("connecting to peer", zap.Error(err))
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
		Logger:    logger.With(zap.Uint64("PeerID", pair.Destination), zap.Bool("Initiator", true)),
		Conn:      connector,
		Initiator: true,
		Peer:      pair.Destination,
	})
	if err != nil {
		logger.Error("handshaking with peer", zap.Error(err))
		return
	}

	rtt, err := p.Ping()
	if err != nil {
		logger.Error("checking for peer rtt", zap.Error(err))
		return
	}

	logger.Info("Peering established", zap.Any("pair", pair), zap.Duration("rtt", rtt))

	fmt.Printf("\n%s\n\n", strings.Repeat("=", 50))
	fmt.Printf("Your Hostname: %+v\n", name)
	fmt.Printf("\n%s\n\n", strings.Repeat("=", 50))

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go p.Start(ctx)
	go func() {
		<-p.NotifyClose()
		logger.Info("peer disconnected, exiting")
		sigs <- syscall.SIGTERM
	}()
	go func() {
		for c := range p.Handle(ctx) {
			o, err := net.Dial("tcp", *forward)
			if err != nil {
				logger.Info("forwarding connection", zap.Error(err), zap.Stringp("destination", forward))
				continue
			}
			errCh := multiplexer.Connect(ctx, c.Conn, o)
			go func() {
				for e := range errCh {
					if multiplexer.IsTimeout(e) {
						continue
					}
					logger.Error("unexpected error in bidirectional stream", zap.Error(e))
				}
			}()
		}
	}()

	<-sigs

	if err := p.Bye(); err != nil {
		logger.Error("cannot close connection with peer", zap.Error(err))
	}
}
