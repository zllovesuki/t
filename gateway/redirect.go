package gateway

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/zllovesuki/t/profiler"

	"go.uber.org/zap"
)

func httpRedirecter(w http.ResponseWriter, r *http.Request) {
	re := url.URL{
		Scheme:   "https",
		Host:     r.Host,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	http.Redirect(w, r, re.String(), http.StatusPermanentRedirect)
	profiler.GatewayRequests.WithLabelValues("success", "redirect").Add(1)
}

func RedirectHTTP(logger *zap.Logger, bindAddr string, webPort int) {
	if webPort != 443 {
		return
	}
	addr := fmt.Sprintf("%s:80", bindAddr)
	logger.Info("starting http redirect server",
		zap.String("addr", addr),
	)
	http.ListenAndServe(addr, http.HandlerFunc(httpRedirecter))
}
