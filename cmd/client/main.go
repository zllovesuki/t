package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zllovesuki/t/client"
	"github.com/zllovesuki/t/multiplexer/protocol"
	_ "github.com/zllovesuki/t/workaround"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Version = "dev"

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	tunnelCommand := flag.NewFlagSet("tunnel", flag.ExitOnError)
	connectCommand := flag.NewFlagSet("connect", flag.ExitOnError)
	forwardCommand := flag.NewFlagSet("forward", flag.ExitOnError)

	target := tunnelCommand.String("peer", "127.0.0.1:11111", "specify the peering target without using autodiscovery")
	where := tunnelCommand.String("where", client.DefaultWhere, "auto discover the peer target given the apex")
	forward := tunnelCommand.String("forward", "http://127.0.0.1:3000", "the http/https forwarding target")
	proto := tunnelCommand.Int("protocol", int(protocol.Mplex), "multiplexer protocol to be used (1: Yamux; 2: Mplex; 3: QUIC) - usually used in debugging")
	tDebug := tunnelCommand.Bool("debug", false, "verbose logging and disable TLS verification")
	overwrite := tunnelCommand.Bool("overwrite", false, "overwrite proxied request Host header with tunnel hostname")

	cConnect := connectCommand.String("url", client.DefaultConnect, "the URL as shown by the tunnel subcommand")
	cDebug := connectCommand.Bool("debug", false, "verbose logging and disable TLS verification")

	fConnect := forwardCommand.String("url", client.DefaultConnect, "the URL as shown by the tunnel subcommand")
	listen := forwardCommand.String("listen", ":5678", "listen for tcp connections and forward them via tunnel")
	fDebug := forwardCommand.Bool("debug", false, "verbose logging and disable TLS verification")

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "expecting subcommands: tunnel, connect, forward\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "tunnel":
		tunnelCommand.Parse(os.Args[2:])
	case "connect":
		connectCommand.Parse(os.Args[2:])
	case "forward":
		forwardCommand.Parse(os.Args[2:])
	default:
		flag.PrintDefaults()
		os.Exit(1)
	}

	var logCfg zap.Config
	var logger *zap.Logger
	var err error
	if *cDebug || *tDebug || *fDebug {
		logCfg = zap.NewDevelopmentConfig()
	} else {
		logCfg = zap.NewProductionConfig()
	}
	logCfg.OutputPaths = []string{"stderr"}
	logger, err = logCfg.Build()
	if err != nil {
		log.Fatal(err)
	}

	// redirect stdlib log output to logger, since some packages
	// are leaking log output and I have no way to redirect them
	undo, err := zap.RedirectStdLogAt(logger, zapcore.DebugLevel)
	if err != nil {
		log.Fatal(err)
	}
	defer undo()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	if tunnelCommand.Parsed() {
		client.Tunnel(ctx, client.TunnelOpts{
			Logger:    logger,
			Forward:   *forward,
			Target:    *target,
			Where:     *where,
			Proto:     *proto,
			Debug:     *tDebug,
			Overwrite: *overwrite,
			Version:   Version,
			Sigs:      sigs,
		})
		return
	}

	if connectCommand.Parsed() {
		if *cConnect == client.DefaultConnect {
			connectCommand.PrintDefaults()
			os.Exit(1)
		}
		client.Connect(ctx, client.ConnectOpts{
			Logger: logger,
			URL:    *cConnect,
			Debug:  *cDebug,
			Sigs:   sigs,
		})
		return
	}

	if forwardCommand.Parsed() {
		if *fConnect == client.DefaultConnect {
			forwardCommand.PrintDefaults()
			os.Exit(1)
		}
		client.Forward(ctx, client.ForwardOpts{
			Logger: logger,
			URL:    *fConnect,
			Addr:   *listen,
			Debug:  *fDebug,
			Sigs:   sigs,
		})
	}

}
