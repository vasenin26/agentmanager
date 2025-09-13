package models

import "time"

type CreateAgentRequest struct {
	Image string `json:"image"`
	Env map[string]string `json:"env,omitempty"`
}

type AgentStatus string

const (
	StatusRunning AgentStatus = "running"
	StatusStopped AgentStatus = "stopped"
	StatusCreated AgentStatus = "created"
)

type AgentInfo struct {
	ID string `json:"id"`
	Image string `json:"image"`
	Status AgentStatus `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}
