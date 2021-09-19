package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/zllovesuki/t/client"
	"github.com/zllovesuki/t/multiplexer/protocol"
	_ "github.com/zllovesuki/t/workaround"

	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Version = "dev"

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {

	var logCfg zap.Config
	var logger *zap.Logger
	var undo func() = func() {}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	after := func(c *cli.Context) error {
		undo()
		time.Sleep(time.Second)
		return nil
	}

	app := &cli.App{
		Version:         Version,
		Usage:           fmt.Sprintf("t tunnel client for %s on %s", runtime.GOARCH, runtime.GOOS),
		Copyright:       "Rachel Chen (@zllovesuki), licensed under MIT.",
		HideHelpCommand: true,
		Description:     "like ngrok, but ambitious",
		ArgsUsage:       " ",
		Flags: []cli.Flag{ // global flags
			&cli.BoolFlag{
				Name:  "debug",
				Value: false,
				Usage: "Enable verbose logging and disable TLS verification",
			},
		},
		Before: func(c *cli.Context) error {
			var err error
			if c.Bool("debug") {
				logCfg = zap.NewDevelopmentConfig()
			} else {
				logCfg = zap.NewProductionConfig()
			}
			logCfg.OutputPaths = []string{"stderr"}
			logger, err = logCfg.Build()
			if err == nil {
				undo, err = zap.RedirectStdLogAt(logger, zapcore.DebugLevel)
			}
			return err
		},
		Commands: []*cli.Command{
			{
				Name:      "tunnel",
				Usage:     "Create a new tunnel to an application of your choosing",
				ArgsUsage: " ",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "where",
						Aliases:  []string{"w"},
						Value:    "tunnel.example.com",
						Usage:    "The hostname of the t instance",
						Required: true,
					},
					&cli.StringFlag{
						Name:    "forward",
						Aliases: []string{"f"},
						Value:   "http://127.0.0.1:3000",
						Usage:   "HTTP/HTTPS/TCP forwarding target",
					},
					&cli.BoolFlag{
						Name:    "overwrite",
						Aliases: []string{"o"},
						Value:   false,
						Usage:   "By default, HTTP/HTTPS requests will be made with -forward's host name. This will overwrite the Host header with tunnel's hostname",
					},
					&cli.IntFlag{
						Name:    "protocol",
						Aliases: []string{"p"},
						Value:   int(protocol.Mplex),
						Usage:   "Specify the protocol for the tunnel. 1: Yamux; 2: mplex; 3: QUIC",
					},
					&cli.BoolFlag{
						Name:    "concise",
						Aliases: []string{"c"},
						Value:   false,
						Usage:   "Print only the hostname to stdout without usage example",
					},
				},
				Action: func(c *cli.Context) error {
					client.Tunnel(c.Context, client.TunnelOpts{
						Logger:    logger,
						AppName:   c.App.Name,
						Forward:   c.String("forward"),
						Where:     c.String("where"),
						Proto:     c.Int("protocol"),
						Debug:     c.Bool("debug"),
						Concise:   c.Bool("concise"),
						Overwrite: c.Bool("overwrite"),
						Version:   Version,
						Sigs:      sigs,
					})
					return nil
				},
				After: after,
			},
			{
				Name:      "connect",
				Usage:     "Proxy TCP connection through the tunnel via stdin/stdout",
				ArgsUsage: " ",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "url",
						Aliases:  []string{"u"},
						Value:    client.DefaultConnect,
						Usage:    "Hostname of your tunnel create by the tunnel subcommand",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					client.Connect(c.Context, client.ConnectOpts{
						Logger: logger,
						URL:    c.String("url"),
						Debug:  c.Bool("debug"),
						Sigs:   sigs,
					})
					return nil
				},
				After: after,
			},
			{
				Name:      "forward",
				Usage:     "Listen for connections locally and forward them via the tunnel",
				ArgsUsage: " ",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "url",
						Aliases:  []string{"u"},
						Value:    client.DefaultConnect,
						Usage:    "Hostname of your tunnel create by the tunnel subcommand",
						Required: true,
					},
					&cli.StringFlag{
						Name:    "listen",
						Aliases: []string{"l"},
						Value:   ":5678",
						Usage:   "Address to bind the listener",
					},
				},
				Action: func(c *cli.Context) error {
					client.Forward(c.Context, client.ForwardOpts{
						Logger: logger,
						URL:    c.String("url"),
						Addr:   c.String("listen"),
						Debug:  c.Bool("debug"),
						Sigs:   sigs,
					})
					return nil
				},
				After: after,
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := app.RunContext(ctx, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%+v\n", err)
	}
}
