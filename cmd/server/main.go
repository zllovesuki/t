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

	addrStr := fmt.Sprintf("%s:%d", *ip, *peerPort)
	listener, err := net.Listen("tcp", addrStr)
	if err != nil {
		fmt.Printf("error listening: %+v\n", err)
		return
	}
	defer listener.Close()

	var gossipers []string
	if len(*members) > 0 {
		gossipers = strings.Split(*members, ",")
	}

	s, err := server.New(server.Config{
		Logger:   logger,
		Listener: listener,
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

	fmt.Printf("server peerID: %d\n", s.PeerID())

	go s.Start(ctx)
	go s.Gossip(ctx)
	go Gateway(ctx, s)

	<-sigs
}
