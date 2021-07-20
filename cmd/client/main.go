package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/peer"
	"github.com/zllovesuki/t/shared"
	_ "github.com/zllovesuki/t/workaround"

	"go.uber.org/zap"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	defaultWhere = "tunnel.example.com"
)

var (
	target  = flag.String("peer", "127.0.0.1:11111", "specify the peering target")
	where   = flag.String("where", defaultWhere, "auto discover the peer target given the apex")
	forward = flag.String("forward", "http://127.0.0.1:3000", "the http/https forwarding target")
	debug   = flag.Bool("debug", false, "verbose logging and disable TLS verification")
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

	if u.Scheme != "http" && u.Scheme != "https" {
		logger.Fatal("only http/https schema is supported", zap.String("schema", u.Scheme))
	}

	peerTarget := *target
	if *where != defaultWhere {
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: *debug,
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

	connector, err := tls.Dial("tcp", peerTarget, &tls.Config{
		InsecureSkipVerify: *debug,
	})
	if err != nil {
		logger.Error("connecting to peer", zap.Error(err))
		return
	}

	pair := multiplexer.Link{}
	buf := pair.Pack()
	n, err := connector.Write(buf)
	if err != nil {
		logger.Error("writing handshake to peer", zap.Error(err))
		return
	}
	if n != multiplexer.LinkSize {
		logger.Error("invalid handshake length sent", zap.Int("length", n))
		return
	}

	n, err = connector.Read(buf)
	if err != nil {
		logger.Error("reading handshake from peer", zap.Error(err))
		return
	}
	if n != multiplexer.LinkSize {
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

	p, err := peer.NewYamuxPeer(peer.YamuxConfig{
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

	// In theory, we could support TCP forwarding. However, since we are doing TLS termination
	// on the Gateway, whatever TCP negotiation protocol will get yeeted by the TLS handshake.
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, e error) {
		logger.Error("forward http/https request", zap.Error(e))
		rw.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(rw, "Forwarding target returned error: %s", e.Error())
	}
	go func() {
		accepter := &MultiplexerAccepter{
			Peer: p,
		}
		http.Serve(accepter, proxy)
	}()

	<-sigs

	if err := p.Bye(); err != nil {
		logger.Error("cannot close connection with peer", zap.Error(err))
	}
}
