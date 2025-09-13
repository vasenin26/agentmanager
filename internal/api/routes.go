package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	promhttp "github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRouter(h *Handlers) http.Handler {
	r := chi.NewRouter()
	r.Post("/agents", h.CreateAgent)
	r.Get("/agents", h.ListAgents)
	r.Post("/agents/{id}/stop", h.StopAgent)
	r.Handle("/metrics", promhttp.Handler())
	return r
}
