package gateway

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"math/rand"
	"net/http"
	"text/template"

	"github.com/zllovesuki/t/profiler"
	"github.com/zllovesuki/t/shared"
)

//go:embed index.md
var tmpl string

//go:embed index.html
var index string

type apexServer struct {
	hostname   string
	host       string
	clientPort int
	mdTmpl     *template.Template
	indexTmpl  *template.Template
}

func (a *apexServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	var err error
	defer func() {
		if err != nil {
			profiler.GatewayRequests.WithLabelValues("error", "apex").Add(1)
		} else {
			profiler.GatewayRequests.WithLabelValues("success", "apex").Add(1)
		}
	}()
	var buf []byte
	var md bytes.Buffer
	defer func() {
		if err != nil {
			w.WriteHeader(http.StatusOK)
		}
	}()
	err = a.mdTmpl.Execute(&md, struct {
		Host   string
		Random string
		Port   int
	}{
		Host:   a.host,
		Random: shared.RandomHostname(),
		Port:   rand.Intn(50000) + 1024,
	})
	if err != nil {
		return
	}
	buf, err = json.Marshal(md.String())
	if err != nil {
		return
	}
	err = a.indexTmpl.Execute(w, struct {
		Content string
	}{
		Content: string(buf[1 : len(buf)-1]),
	})
}

func (a *apexServer) handleLookup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(shared.Where{
		Addr: a.hostname,
		Port: a.clientPort,
	})
	profiler.GatewayRequests.WithLabelValues("success", "lookup").Add(1)
}

func (a *apexServer) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", a.handleRoot)
	m.HandleFunc("/where", a.handleLookup)
	return m
}
