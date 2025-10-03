package models

import (
	"github.com/google/uuid"
)

// AgentMeta represents agent metadata
type AgentMeta struct {
	Server   string `json:"server"`
	AgentID  string `json:"agentId"`
	PublicKey string `json:"publicKey"`
}

// ConfigOptions represents configuration for agent
type ConfigOptions struct {
	AgentID uuid.UUID `json:"agentId"`
	Token   string    `json:"token"`
}

