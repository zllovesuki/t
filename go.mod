module github.com/zllovesuki/t

go 1.16

require (
	github.com/eggsampler/acme/v3 v3.2.1
	github.com/gookit/config/v2 v2.0.25
	github.com/hashicorp/memberlist v0.2.4
	github.com/libp2p/go-mplex v0.3.1-0.20210721191624-fc8b95830f5c
	github.com/libp2p/go-yamux/v2 v2.2.0
	github.com/lucas-clemente/quic-go v0.23.0
	github.com/miekg/dns v1.1.43
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/sethvargo/go-diceware v0.2.1
	go.uber.org/zap v1.19.1
	golang.org/x/sys v0.0.0-20210915083310-ed5796bab164
)

replace github.com/hashicorp/memberlist => github.com/miragespace/memberlist v0.2.5-0.20210723090033-7b76c3050af2
