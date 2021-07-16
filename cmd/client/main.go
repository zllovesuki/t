package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
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
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

var (
	peer    = flag.String("peer", "127.0.0.1:11111", "server")
	forward = flag.String("forward", "127.0.0.1:3000", "where to forward")
)

func main() {
	flag.Parse()

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector, err := tls.Dial("tcp", *peer, nil)
	if err != nil {
		logger.Error("connecting to peer", zap.Error(err))
		return
	}

	pair := multiplexer.Pair{}
	buf := pair.Pack()
	n, err := connector.Write(buf)
	if err != nil {
		logger.Error("writing handshake to peer", zap.Error(err))
		return
	}
	if n != multiplexer.PairSize {
		logger.Error("invalid handshake length sent", zap.Int("length", n))
		return
	}

	n, err = connector.Read(buf)
	if err != nil {
		logger.Error("reading handshake from peer", zap.Error(err))
		return
	}
	if n != multiplexer.PairSize {
		logger.Error("invalid handshake length received", zap.Int("length", n))
		return
	}
	pair.Unpack(buf)

	var g shared.GeneratedName
	err = json.NewDecoder(connector).Decode(&g)
	if err != nil {
		logger.Error("unmarshaling generated name response")
		return
	}

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
	fmt.Printf("Your Hostname: %+v\n", g.Hostname)
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
