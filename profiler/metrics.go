package profiler

import "github.com/prometheus/client_golang/prometheus"

var (
	GatewayRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "t",
		Subsystem: "server",
		Help:      "Count of all requests made to the gateway",
		Name:      "gateway_requests_total",
	}, []string{"status", "type"})

	GatewayReqsType = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "t",
		Subsystem: "server",
		Help:      "Count of all forwarded tunnel types",
		Name:      "gateway_tunnel_type",
	}, []string{"type"})

	ConnectionStats = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "t",
		Subsystem: "server",
		Help:      "Stats on multiplexer connections",
		Name:      "multiplexer_stats",
	}, []string{"desc", "type"})

	PeerLatencyHist = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "t",
		Subsystem: "server",
		Help:      "Histogram of peers latency via gossip",
		Name:      "peer_rtt_ms",
	})
)
