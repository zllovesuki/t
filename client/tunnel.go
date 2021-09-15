package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/alpn"
	"github.com/zllovesuki/t/multiplexer/protocol"
	"github.com/zllovesuki/t/shared"
	"github.com/zllovesuki/t/state"

	"go.uber.org/zap"
)

const (
	DefaultWhere = "tunnel.example.com"
)

type TunnelOpts struct {
	Logger    *zap.Logger
	Forward   string
	Target    string
	Where     string
	Proto     int
	Debug     bool
	Overwrite bool
	Version   string
	Sigs      chan os.Signal
}

func Tunnel(ctx context.Context, opts TunnelOpts) {
	logger := opts.Logger

	u, err := url.Parse(opts.Forward)
	if err != nil {
		logger.Fatal("parsing forwarding target", zap.Error(err))
	}

	switch u.Scheme {
	case "http", "https", "tcp":
	default:
		logger.Fatal("unsupported scheme. valid schemes: http, https, tcp", zap.String("schema", u.Scheme))
	}

	peerTarget := opts.Target
	if opts.Where != DefaultWhere {
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: opts.Debug,
					NextProtos:         []string{"http/1.1"},
				},
			},
			Timeout: time.Second * 5,
		}
		resp, err := client.Get(fmt.Sprintf("https://%s/where", opts.Where))
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

	var connector interface{}
	var conn net.Conn
	var link multiplexer.Link
	var g shared.GeneratedName

	proposedProtocol := protocol.Protocol(opts.Proto)
	dialer, err := multiplexer.Dialer(proposedProtocol)
	if err != nil {
		logger.Fatal("selecting peer dialer", zap.Error(err))
	}

	logger.Info("Protocol proposal", zap.String("protocol", proposedProtocol.String()), zap.String("clientVersion", opts.Version))

	connector, conn, _, err = dialer(peerTarget, &tls.Config{
		InsecureSkipVerify: opts.Debug,
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

	tunnelURL, _ := url.Parse(g.Hostname)
	hostHeader := u.Hostname()
	if opts.Overwrite {
		hostHeader = tunnelURL.Hostname()
	}

	fmt.Printf("\n%s\n\n", strings.Repeat("=", 50))
	switch u.Scheme {
	case "http", "https":
		fmt.Printf("HTTPS requests will be forwarded to: %+v (Host: %s)\n", opts.Forward, hostHeader)
	case "tcp":
		fmt.Printf("TCP connections will be forwarded to: %+v\n\n", opts.Forward)
		fmt.Printf("Example usages:\n\n")
		fmt.Printf("SSH:\n")
		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		fmt.Printf("ssh -o ServerAliveInterval=15 -o ProxyCommand=\".%ct-client-%s-%s%s connect -url %s\" user@127.0.0.1\n", os.PathSeparator, runtime.GOOS, runtime.GOARCH, ext, g.Hostname)
	default:
	}
	fmt.Printf("\nYour Hostname: %+v\n\n", g.Hostname)

	go p.Start(ctx)
	go func() {
		<-p.NotifyClose()
		logger.Info("peer disconnected, exiting")
		opts.Sigs <- syscall.SIGTERM
	}()

	proxy := httputil.NewSingleHostReverseProxy(u)
	d := proxy.Director
	// https://stackoverflow.com/a/53007606
	// need to overwrite Host field
	proxy.Director = func(r *http.Request) {
		d(r)
		r.Host = hostHeader
	}
	proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: opts.Debug,
		},
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, e error) {
		logger.Error("forwarding http/https request", zap.Error(e))
		rw.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(rw, "Forwarding target returned error: %s", e.Error())
	}
	proxy.ErrorLog = zap.NewStdLog(opts.Logger)

	c := make(chan net.Conn, 32)
	go func() {
		accepter := &httpAccepter{
			ch: c,
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
				dialer := &net.Dialer{
					Timeout: time.Second * 3,
				}
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

	<-opts.Sigs
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
	buf, err := link.MarshalBinary()
	if err != nil {
		logger.Fatal("marshal link", zap.Error(err))
	}

	n, err := connector.Write(buf)
	if err != nil {
		logger.Fatal("writing handshake to peer", zap.Error(err))
		return
	}
	if n != multiplexer.LinkSize {
		logger.Fatal("invalid handshake length sent", zap.Int("length", n))
		return
	}

	_, err = connector.Read(buf)
	if err != nil {
		logger.Fatal("reading handshake from peer", zap.Error(err))
		return
	}

	if err := link.UnmarshalBinary(buf); err != nil {
		logger.Fatal("unmarshal link", zap.Error(err))
		return
	}

	err = json.NewDecoder(connector).Decode(&g)
	if err != nil {
		logger.Fatal("unmarshaling generated name response")
		return
	}

	return
}
