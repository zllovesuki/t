package gateway

import (
	"net/http"
)

type apexServer struct{}

func (a *apexServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "yeet.html")
}

func (a *apexServer) handleLookup(w http.ResponseWriter, r *http.Request) {

}

func (a *apexServer) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", a.handleRoot)
	m.HandleFunc("/where", a.handleLookup)
	return m
}
