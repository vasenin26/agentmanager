package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/agent-svc/internal/models"
	"github.com/yourorg/agent-svc/internal/service"
)

type Handlers struct{ svc *service.AgentService }
func NewHandlers(s *service.AgentService) *Handlers { return &Handlers{svc: s} }

func (h *Handlers) CreateAgent(w http.ResponseWriter, r *http.Request) {
	var req models.CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "invalid body", http.StatusBadRequest); return }
	if req.Image == "" { http.Error(w, "image is required", http.StatusBadRequest); return }
	ai, err := h.svc.CreateAndStart(r.Context(), req); if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
	w.WriteHeader(http.StatusCreated); json.NewEncoder(w).Encode(ai)
}

func (h *Handlers) ListAgents(w http.ResponseWriter, r *http.Request) {
	agents, _ := h.svc.ListRunning(r.Context()); json.NewEncoder(w).Encode(agents)
}

func (h *Handlers) StopAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" { http.Error(w, "id required", http.StatusBadRequest); return }
	ai, err := h.svc.Stop(r.Context(), id); if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
	json.NewEncoder(w).Encode(ai)
}
