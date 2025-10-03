package interfaces

import "github.com/vasenin26/agentmanager/internal/models"

// AgentOrchestratorInterface defines the contract for agent orchestration
type AgentOrchestratorInterface interface {
	StartAgent(configOptions models.ConfigOptions) models.AgentMeta
	StopAgent(agentID string) error
	StartProcess(taskType string) error
}
