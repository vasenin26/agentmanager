package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	promhttp "github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRouter(h *Handlers) http.Handler {
	r := chi.NewRouter()
	
	// Orchestrator interface endpoints
	r.Post("/orchestrator/start-agent", h.StartAgent)
	r.Post("/orchestrator/stop-agent/{agentId}", h.StopAgent)
	r.Post("/orchestrator/start-process", h.StartProcess)
	
	// SSH key management
	r.Get("/ssh/keys/{agentId}", h.GetSSHKey)
	
	r.Handle("/metrics", promhttp.Handler())
	return r
}
