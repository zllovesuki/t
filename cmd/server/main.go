package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server"
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

	addrStr := fmt.Sprintf("%s:%d", *ip, *peerPort)
	listener, err := net.Listen("tcp", addrStr)
	if err != nil {
		fmt.Printf("error listening: %+v\n", err)
		return
	}
	defer listener.Close()

	s, err := server.NewServer(listener)
	if err != nil {
		panic(err)
	}

	fmt.Printf("server peerID: %d\n", s.PeerID())

	go s.Start(ctx)

	c := memberlist.DefaultLocalConfig()
	c.AdvertiseAddr = *ip
	c.AdvertisePort = *gossipPort
	c.BindAddr = *ip
	c.BindPort = *gossipPort
	c.Events = s
	c.Name = fmt.Sprint(s.PeerID())
	c.LogOutput = io.Discard

	m, err := memberlist.Create(c)
	if err != nil {
		panic(err)
	}
	defer m.Shutdown()
	defer m.Leave(time.Second * 3)

	if len(*members) > 0 {
		parts := strings.Split(*members, ",")
		if _, err := m.Join(parts); err != nil {
			panic(err)
		}
	}

	node := m.LocalNode()
	node.Meta = s.Meta()
	// fmt.Printf("local node: %s:%d\n", node.Addr, node.Port)

	cert, err := tls.LoadX509KeyPair("tls/dev.pem", "tls/dev-key.pem")
	if err != nil {
		panic(err)
	}

	config := tls.Config{
		Certificates: []tls.Certificate{cert},
		Rand:         rand.Reader,
	}

	xd, err := tls.Listen("tcp", fmt.Sprintf("%s:%d", *ip, *peerPort-10), &config)
	if err != nil {
		panic(err)
	}
	handle := func(conn *tls.Conn) {
		var err error
		var clientID int64
		defer func() {
			if err != nil {
				fmt.Printf("error in connection: %+v\n", err)
			}
		}()
		err = conn.Handshake()
		if err != nil {
			fmt.Printf("handshake failed: %+v\n", err)
			return
		}
		xd := strings.Split(conn.ConnectionState().ServerName, ".")
		clientID, err = strconv.ParseInt(xd[0], 10, 64)
		if err != nil {
			fmt.Printf("fail to parse clientID: %+v\n", err)
		}
		err = s.Open(ctx, conn, multiplexer.Pair{
			Source:      s.PeerID(),
			Destination: clientID,
		})
	}
	go func() {
		for {
			conn, err := xd.Accept()
			if err != nil {
				fmt.Printf("error accepting tls connection: %+v\n", err)
				return
			}
			tconn := conn.(*tls.Conn)
			go handle(tconn)
		}
	}()

	// go func() {
	// 	l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", *ip, *peerPort-10))
	// 	if err != nil {
	// 		fmt.Printf("no: %+v\n", err)
	// 		return
	// 	}
	// 	for {
	// 		conn, err := l.Accept()
	// 		if err != nil {
	// 			fmt.Printf("accept: %+v\n", err)
	// 			return
	// 		}
	// 		if err := s.Open(ctx, conn, 5577006791947779410); err != nil {
	// 			fmt.Printf("open: %+v\n", err)
	// 			conn.Close()
	// 		}
	// 	}
	// }()

	<-ctx.Done()
}
