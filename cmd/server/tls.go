package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/server"
)

func Gateway(ctx context.Context, s *server.Server) {
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
	for {
		conn, err := xd.Accept()
		if err != nil {
			fmt.Printf("error accepting tls connection: %+v\n", err)
			return
		}
		tconn := conn.(*tls.Conn)
		go handleTLS(ctx, s, tconn)
	}
}

func handleTLS(ctx context.Context, s *server.Server, conn *tls.Conn) {
	var err error
	var clientID uint64
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
	h := fnv.New64a()
	h.Write([]byte(xd[0]))
	clientID = h.Sum64()
	err = s.Open(ctx, conn, multiplexer.Pair{
		Source:      s.PeerID(),
		Destination: clientID,
	})
	if err != nil {
		conn.Close()
	}
}
