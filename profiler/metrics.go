package profiler

import "github.com/prometheus/client_golang/prometheus"

var (
	GatewayRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "t",
		Subsystem: "server",
		Help:      "Count of all requests made to the gateway",
		Name:      "gateway_requests_total",
	}, []string{"status", "type"})

	ConnectionStats = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "t",
		Subsystem: "server",
		Help:      "Stats on multiplexer connections",
		Name:      "multiplexer_stats",
	}, []string{"desc", "type"})
)
