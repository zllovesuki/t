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
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/alpn"
	"github.com/zllovesuki/t/multiplexer/protocol"
	"github.com/zllovesuki/t/shared"
	"github.com/zllovesuki/t/state"
	_ "github.com/zllovesuki/t/workaround"

	"go.uber.org/zap"
)

var Version = "dev"

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	defaultWhere = "tunnel.example.com"
)

var (
	target  = flag.String("peer", "127.0.0.1:11111", "specify the peering target without using autodiscovery")
	where   = flag.String("where", defaultWhere, "auto discover the peer target given the apex")
	forward = flag.String("forward", "http://127.0.0.1:3000", "the http/https forwarding target")
	debug   = flag.Bool("debug", false, "verbose logging and disable TLS verification")
	proto   = flag.Int("protocol", int(protocol.Mplex), "multiplexer protocol to be used (1: Yamux; 2: Mplex; 3: QUIC) - usually used in debugging")
)

func main() {
	flag.Parse()

	var logger *zap.Logger
	var err error
	if *debug {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}
	if err != nil {
		log.Fatal(err)
	}

	u, err := url.Parse(*forward)
	if err != nil {
		logger.Fatal("parsing forwarding target", zap.Error(err))
	}

	switch u.Scheme {
	case "http", "https", "tcp":
	default:
		logger.Fatal("unsupported scheme. valid schemes: http, https, tcp", zap.String("schema", u.Scheme))
	}

	peerTarget := *target
	if *where != defaultWhere {
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: *debug,
					NextProtos:         []string{"http/1.1"},
				},
			},
			Timeout: time.Second * 5,
		}
		resp, err := client.Get(fmt.Sprintf("https://%s/where", *where))
		if err != nil {
			logger.Fatal("auto discovering peer", zap.Error(err))
		}
		if resp.StatusCode != http.StatusOK {
			logger.Fatal("auto discovering returns non-200 response code", zap.Int("code", resp.StatusCode))
		}
		var w shared.Where
		if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
			logger.Fatal("cannot recode auto discovering result", zap.Error(err))
		}
		resp.Body.Close()
		peerTarget = fmt.Sprintf("%s:%d", w.Addr, w.Port)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var connector interface{}
	var conn net.Conn
	var link multiplexer.Link
	var g shared.GeneratedName

	proposedProtocol := protocol.Protocol(*proto)
	dialer, err := multiplexer.Dialer(proposedProtocol)
	if err != nil {
		logger.Fatal("selecting peer dialer", zap.Error(err))
	}

	logger.Info("Protocol proposal", zap.String("protocol", proposedProtocol.String()), zap.String("clientVersion", Version))

	connector, conn, _, err = dialer(peerTarget, &tls.Config{
		InsecureSkipVerify: *debug,
		NextProtos:         []string{alpn.Multiplexer.String()},
	})
	if err != nil {
		logger.Fatal("connecting to peer", zap.Error(err))
	}

	link, g = peerNegotiation(logger, conn, proposedProtocol)

	pm := state.NewPeerMap(logger, link.Source)

	err = pm.NewPeer(ctx, link.Protocol, multiplexer.Config{
		Logger:    logger.With(zap.Uint64("PeerID", link.Destination), zap.Bool("Initiator", true)),
		Conn:      connector,
		Initiator: true,
		Peer:      link.Destination,
	})
	if err != nil {
		logger.Fatal("registering peer", zap.Error(err))
	}

	var p multiplexer.Peer
	select {
	case p = <-pm.Notify():
		logger.Info("Peering established", zap.Object("link", link))
	case <-time.After(time.Second * 3):
		logger.Fatal("timeout attempting to establish connection with peer")
	}

	fmt.Printf("\n%s\n\n", strings.Repeat("=", 50))
	fmt.Printf("Your Hostname: %+v\n\n", g.Hostname)
	fmt.Printf("Requests will be forwarded to: %+v\n", *forward)
	fmt.Printf("\n%s\n\n", strings.Repeat("=", 50))

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go p.Start(ctx)
	go func() {
		<-p.NotifyClose()
		logger.Info("peer disconnected, exiting")
		sigs <- syscall.SIGTERM
	}()

	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, e error) {
		logger.Error("forward http/https request", zap.Error(e))
		rw.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(rw, "Forwarding target returned error: %s", e.Error())
	}
	c := make(chan net.Conn, 32)
	go func() {
		accepter := &multiplexerAccepter{
			ConnCh: c,
		}
		http.Serve(accepter, proxy)
	}()

	go func() {
		for l := range p.Handle() {
			if !alpnCheck(l.Link.ALPN, u) {
				logger.Error("invalid alpn for forwarding", zap.String("scheme", u.Scheme), zap.String("ALPN", l.Link.ALPN.String()))
				l.Conn.Close()
				continue
			}
			switch l.Link.ALPN {
			default:
				c <- l.Conn
			case alpn.Multiplexer:
				logger.Fatal("received alpn proposal for multiplexer on forward target")
			case alpn.Raw:
				dialer := &net.Dialer{}
				n, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%s", u.Hostname(), u.Port()))
				if err != nil {
					logger.Error("forwarding raw connection", zap.Error(err))
					l.Conn.Close()
					continue
				}
				multiplexer.Connect(ctx, n, l.Conn)
			}
		}
	}()

	<-sigs
	pm.Remove(p.Peer())
}

func alpnCheck(a alpn.ALPN, u *url.URL) bool {
	switch u.Scheme {
	case "http", "https":
		return a == alpn.HTTP
	case "tcp":
		return a == alpn.Raw
	default:
		return false
	}
}

func peerNegotiation(logger *zap.Logger, connector net.Conn, proto protocol.Protocol) (link multiplexer.Link, g shared.GeneratedName) {
	link = multiplexer.Link{
		Protocol: protocol.Protocol(proto),
		ALPN:     alpn.Multiplexer,
	}
	buf := link.Pack()
	n, err := connector.Write(buf)
	if err != nil {
		logger.Fatal("writing handshake to peer", zap.Error(err))
		return
	}
	if n != multiplexer.LinkSize {
		logger.Fatal("invalid handshake length sent", zap.Int("length", n))
		return
	}

	n, err = connector.Read(buf)
	if err != nil {
		logger.Fatal("reading handshake from peer", zap.Error(err))
		return
	}
	if n != multiplexer.LinkSize {
		logger.Fatal("invalid handshake length received", zap.Int("length", n))
		return
	}
	link.Unpack(buf)

	err = json.NewDecoder(connector).Decode(&g)
	if err != nil {
		logger.Fatal("unmarshaling generated name response")
		return
	}

	return
}
