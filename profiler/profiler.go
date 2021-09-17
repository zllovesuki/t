package profiler

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func StartProfiler(addr string) {
	m := http.NewServeMux()

	r := prometheus.NewRegistry()
	r.MustRegister(GatewayRequests)
	r.MustRegister(ConnectionStats)

	m.Handle("/", debug())
	m.Handle("/metrics", promhttp.HandlerFor(r, promhttp.HandlerOpts{}))

	http.ListenAndServe(addr, m)
}
