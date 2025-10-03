package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vasenin26/agentmanager/internal/models"
	"github.com/vasenin26/agentmanager/internal/interfaces"
)

type Handlers struct{ 
	svc interfaces.AgentOrchestratorInterface 
}
func NewHandlers(s interfaces.AgentOrchestratorInterface) *Handlers { return &Handlers{svc: s} }


// StartAgent starts an agent using the orchestrator interface
func (h *Handlers) StartAgent(w http.ResponseWriter, r *http.Request) {
	var configOptions models.ConfigOptions
	if err := json.NewDecoder(r.Body).Decode(&configOptions); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	
	agentMeta := h.svc.StartAgent(configOptions)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(agentMeta)
}

// StopAgent stops an agent using the orchestrator interface
func (h *Handlers) StopAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentId")
	if agentID == "" {
		http.Error(w, "agentId is required", http.StatusBadRequest)
		return
	}
	
	if err := h.svc.StopAgent(agentID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "agent stopped"})
}

// StartProcess starts a process using the orchestrator interface
func (h *Handlers) StartProcess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskType string `json:"taskType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	
	if err := h.svc.StartProcess(req.TaskType); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "process started"})
}

// GetSSHKey retrieves SSH key pair by agent ID
func (h *Handlers) GetSSHKey(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentId")
	if agentID == "" {
		http.Error(w, "agentId is required", http.StatusBadRequest)
		return
	}
	
	// This would need to be implemented in the service layer
	// For now, return a placeholder response
	http.Error(w, "SSH key retrieval not implemented", http.StatusNotImplemented)
}
