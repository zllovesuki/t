module github.com/zllovesuki/t

go 1.16

require (
	github.com/eggsampler/acme/v3 v3.2.1
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/gookit/config/v2 v2.0.24
	github.com/hashicorp/go-multierror v1.1.0 // indirect
	github.com/hashicorp/go-uuid v1.0.1 // indirect
	github.com/hashicorp/memberlist v0.2.4
	github.com/libp2p/go-mplex v0.3.1-0.20210721191624-fc8b95830f5c
	github.com/libp2p/go-yamux/v2 v2.2.0
	github.com/miekg/dns v1.1.43
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/sethvargo/go-diceware v0.2.1
	go.uber.org/atomic v1.8.0 // indirect
	go.uber.org/multierr v1.7.0 // indirect
	go.uber.org/zap v1.18.1
	golang.org/x/lint v0.0.0-20201208152925-83fdc39ff7b5 // indirect
	golang.org/x/net v0.0.0-20210614182718-04defd469f4e // indirect
	golang.org/x/sys v0.0.0-20210615035016-665e8c7367d1 // indirect
	golang.org/x/tools v0.1.5 // indirect
)

replace github.com/hashicorp/memberlist => github.com/miragespace/memberlist v0.2.5
