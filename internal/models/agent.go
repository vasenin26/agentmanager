package models

import (
	"github.com/google/uuid"
)

// AgentMeta represents agent metadata
type AgentMeta struct {
	AgentID   string `json:"agentId"`
	PublicKey string `json:"publicKey"`
}

// ConfigOptions represents configuration for agent
type ConfigOptions struct {
	AgentID uuid.UUID `json:"agentId"`
	Token   string    `json:"token"`
}
