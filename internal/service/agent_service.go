package service

import (
	"context"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
	"github.com/vasenin26/agentmanager/internal/interfaces"
	"github.com/vasenin26/agentmanager/internal/metrics"
	"github.com/vasenin26/agentmanager/internal/models"
	"github.com/vasenin26/agentmanager/internal/ssh"
)

type AgentService struct {
	dc             docker.DockerClient
	registry       docker.AuthConfig
	defaultTimeout time.Duration
	serverURL      string
	// SSH key storage
	sshStorage *ssh.Storage
	// Agent configuration
	apiHost      string
	openaiModel  string
	openaiApiKey string
	gitUserName  string
	gitUserEmail string
}

// Ensure AgentService implements AgentOrchestratorInterface
var _ interfaces.AgentOrchestratorInterface = (*AgentService)(nil)

func NewAgentService(dc docker.DockerClient, reg docker.AuthConfig, t time.Duration, serverURL string, sshStorage *ssh.Storage, apiHost, openaiModel, openaiApiKey, gitUserName, gitUserEmail string) *AgentService {
	return &AgentService{
		dc:             dc,
		registry:       reg,
		defaultTimeout: t,
		serverURL:      serverURL,
		sshStorage:     sshStorage,
		apiHost:        apiHost,
		openaiModel:    openaiModel,
		openaiApiKey:   openaiApiKey,
		gitUserName:    gitUserName,
		gitUserEmail:   gitUserEmail,
	}
}

// StartAgent implements AgentOrchestratorInterface
func (as *AgentService) StartAgent(configOptions models.ConfigOptions) models.AgentMeta {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, as.defaultTimeout)
	defer cancel()

	// Check if SSH keys already exist for this agent
	sshKeyPair, err := as.sshStorage.GetKeyPair(configOptions.AgentID.String())
	if err != nil {
		// Keys don't exist, generate new ones
		sshKeyPair, err = as.sshStorage.GenerateAndStoreKeyPair(configOptions.AgentID.String())
		if err != nil {
			// Return empty meta on error - in production you might want to handle this differently
			return models.AgentMeta{}
		}
	}

	// Use the agentmodule image
	image := "ghcr.io/vasenin26/agentmodule"

	// Pull the image
	if err := as.dc.PullImage(ctx, image, as.registry); err != nil {
		// Log error but continue - in production you might want to handle this differently
	}

	// Create container with agent configuration
	containerConfig := docker.ContainerConfig{
		Image: image,
		Env: map[string]string{
			"AGENT_ID":        configOptions.AgentID.String(),
			"API_TOKEN":       configOptions.Token,
			"SSH_PRIVATE_KEY": sshKeyPair.PrivateKey,
			"API_HOST":        as.apiHost,
			"OPENAI_MODEL":    as.openaiModel,
			"OPENAI_API_KEY":  as.openaiApiKey,
			"GIT_USER_NAME":   as.gitUserName,
			"GIT_USER_EMAIL":  as.gitUserEmail,
		},
	}

	containerID, err := as.dc.CreateContainer(ctx, containerConfig)
	if err != nil {
		// Return empty meta on error - in production you might want to handle this differently
		return models.AgentMeta{}
	}

	// Start the container
	if err := as.dc.StartContainer(ctx, containerID); err != nil {
		// Return empty meta on error - in production you might want to handle this differently
		return models.AgentMeta{}
	}

	// Инкрементируем метрики
	metrics.ContainerStartCommands.Inc()
	metrics.CreatedAgents.Inc()

	return models.AgentMeta{
		Server:    as.serverURL,
		AgentID:   configOptions.AgentID.String(),
		PublicKey: sshKeyPair.PublicKey,
	}
}

// StopAgent implements AgentOrchestratorInterface
func (as *AgentService) StopAgent(agentID string) error {
	// SSH keys are preserved for potential agent restart
	// In a real implementation, you would stop the Docker container here
	// For now, just return nil to indicate success

	// Инкрементируем метрику остановки контейнера
	metrics.ContainerStopCommands.Inc()

	return nil
}

// StartProcess implements AgentOrchestratorInterface
func (as *AgentService) StartProcess(taskType string) error {
	// In a real implementation, you would start a process based on taskType
	// For now, just return nil

	// Инкрементируем метрику запуска процессинга
	metrics.ProcessStartCommands.Inc()

	return nil
}
