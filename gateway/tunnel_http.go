package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/zllovesuki/t/multiplexer"
	"github.com/zllovesuki/t/multiplexer/alpn"
	"github.com/zllovesuki/t/profiler"
	"github.com/zllovesuki/t/shared"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (g *Gateway) httpHandler() http.Handler {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = req.TLS.ServerName
		},
		Transport: &http.Transport{
			DialContext: func(c context.Context, network, addr string) (net.Conn, error) {
				return g.Multiplexer.Direct(c, g.link(addr, alpn.HTTP.String()))
			},
			MaxConnsPerHost:       30,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       shared.ConnIdleTimeout,
			ResponseHeaderTimeout: time.Second * 30,
			ExpectContinueTimeout: time.Second * 3,
		},
		BufferPool:   newBufferPool(),
		ErrorHandler: g.errorHandler,
		ModifyResponse: func(r *http.Response) error {
			// since visitor and client shares the same port, proxied target may have
			// alt-svc set in their response, which will either break peer quic,
			// or the visitor won't be able to visit
			r.Header.Del("alt-svc")
			profiler.GatewayRequests.WithLabelValues("success", "forward").Add(1)
			return nil
		},
		ErrorLog: zap.NewStdLog(g.Logger),
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
	if errors.Is(e, context.Canceled) {
		// this is expected
		return
	}
	g.Logger.Error("forwarding http/https request", zap.Error(e))
	rw.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprint(rw, "An unexpected error has occurred while attempting to forward to destination.")
	profiler.GatewayRequests.WithLabelValues("error", "forward").Add(1)
}
