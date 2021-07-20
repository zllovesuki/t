package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/profiler"
	"github.com/zllovesuki/t/shared"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (g *Gateway) tunnelHandler() http.Handler {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = req.TLS.ServerName
		},
		Transport: &http.Transport{
			DialContext: func(c context.Context, network, addr string) (net.Conn, error) {
				parts := strings.SplitN(addr, ".", 2)
				clientID := shared.PeerHash(parts[0])
				return g.Multiplexer.Direct(c, multiplexer.Link{
					Source:      g.Multiplexer.PeerID(),
					Destination: clientID,
				})
			},
			MaxConnsPerHost:       30,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       time.Second * 60,
			ResponseHeaderTimeout: time.Second * 30,
			ExpectContinueTimeout: time.Second * 3,
		},
		BufferPool:   newBufferPool(),
		ErrorHandler: g.errorHandler,
	}
}

func (g *Gateway) errorHandler(rw http.ResponseWriter, r *http.Request, e error) {
	if errors.Is(e, multiplexer.ErrDestinationNotFound) {
		rw.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(rw, "Destination %s not found. Propagation may take up to 2 minutes.", r.URL.Hostname())
		profiler.GatewayRequests.WithLabelValues("not_found", "forward").Add(1)
		return
	}
	if multiplexer.IsTimeout(e) {
		rw.WriteHeader(http.StatusGatewayTimeout)
		fmt.Fprintf(rw, "Destination %s is taking too long to respond.", r.URL.Hostname())
		profiler.GatewayRequests.WithLabelValues("timeout", "forward").Add(1)
		return
	}
	g.Logger.Error("forwarding http/https request", zap.Error(e))
	rw.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprint(rw, "An unexpected error has occurred while attempting to forward to destination.")
	profiler.GatewayRequests.WithLabelValues("error", "forward").Add(1)
}
