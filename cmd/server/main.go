package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/zllovesuki/t/server"
	"go.uber.org/zap"
)

var (
	members    = flag.String("members", "", "comma seperated list of members")
	ip         = flag.String("ip", "127.0.0.1", "node ip")
	peerPort   = flag.Int("peerPort", 11111, "multiplexer peer port")
	clientPort = flag.Int("clientPort", 9999, "port used for client")
	gossipPort = flag.Int("gossipPort", 10101, "port used for memberlist")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	peerAddr := fmt.Sprintf("%s:0", *ip)
	clientAddr := fmt.Sprintf("%s:%d", *ip, *clientPort)
	peerListener, err := net.Listen("tcp", peerAddr)
	if err != nil {
		fmt.Printf("error listening for peers: %+v\n", err)
		return
	}
	defer peerListener.Close()
	clientListener, err := net.Listen("tcp", clientAddr)
	if err != nil {
		fmt.Printf("error listening for ckient: %+v\n", err)
		return
	}
	defer clientListener.Close()

	var gossipers []string
	if len(*members) > 0 {
		gossipers = strings.Split(*members, ",")
	}

	s, err := server.New(server.Config{
		Logger:         logger,
		PeerListener:   peerListener,
		ClientListener: clientListener,
		Gossip: struct {
			IP      string
			Port    int
			Members []string
		}{
			IP:      *ip,
			Port:    *gossipPort,
			Members: gossipers,
		},
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("%+v\n", string(s.Meta()))

	fmt.Printf("server peerID: %d\n", s.PeerID())

	s.Start(ctx)
	if err := s.Gossip(ctx); err != nil {
		panic(err)
	}
	go Gateway(ctx, s)

	<-sigs
}
