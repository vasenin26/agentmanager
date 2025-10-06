package service

import (
	"context"
	"os"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
	"github.com/vasenin26/agentmanager/internal/models"
)

type AgentService struct {
	dc             docker.DockerClient
	registry       docker.AuthConfig
	defaultTimeout time.Duration
	serverURL      string
	// Agent configuration
	apiHost      string
	openaiModel  string
	openaiApiKey string
	gitUserName  string
	gitUserEmail string
}

func NewAgentService(dc docker.DockerClient, reg docker.AuthConfig, t time.Duration, serverURL string, apiHost, openaiModel, openaiApiKey, gitUserName, gitUserEmail string) *AgentService {
	return &AgentService{
		dc:             dc,
		registry:       reg,
		defaultTimeout: t,
		serverURL:      serverURL,
		apiHost:        apiHost,
		openaiModel:    openaiModel,
		openaiApiKey:   openaiApiKey,
		gitUserName:    gitUserName,
		gitUserEmail:   gitUserEmail,
	}
}

// StartAgentForTask запускает агента для конкретной задачи с опциональным контекстом
func (as *AgentService) StartAgentForTask(
	configOptions models.ConfigOptions,
	taskID string,
	agentUUID string, // UUID воркера для резервирования
	contextVolumeID *string,
	memoryLimit int64,
	projectPrivateKey string, // Приватный ключ проекта
) (models.AgentMeta, error) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, as.defaultTimeout)
	defer cancel()

	// Image
	image := os.Getenv("AGENT_IMAGE")
	if image == "" {
		image = "ghcr.io/vasenin26/agentmodule"
	}

	// Pull image
	if err := as.dc.PullImage(ctx, image, as.registry); err != nil {
		// Log but continue
	}

	// Подготовить volumes
	var volumes []docker.VolumeMount
	if contextVolumeID != nil {
		volumes = append(volumes, docker.VolumeMount{
			VolumeID:  *contextVolumeID,
			MountPath: "/repos",
		})
	}

	// Create container config
	containerConfig := docker.ContainerConfig{
		Image:       image,
		MemoryLimit: memoryLimit,
		Volumes:     volumes,
		Env: map[string]string{
			"AGENT_ID":        configOptions.AgentID.String(),
			"AGENT_UUID":      agentUUID, // UUID воркера для резервирования
			"TASK_ID":         taskID,    // ID задачи для получения деталей
			"API_TOKEN":       configOptions.Token,
			"SSH_PRIVATE_KEY": projectPrivateKey, // ВАЖНО: это ключ ПРОЕКТА, не агента!
			"API_HOST":        as.apiHost,
			"OPENAI_MODEL":    as.openaiModel,
			"OPENAI_API_KEY":  as.openaiApiKey,
			"GIT_USER_NAME":   as.gitUserName,
			"GIT_USER_EMAIL":  as.gitUserEmail,
		},
	}

	containerID, err := as.dc.CreateContainer(ctx, containerConfig)
	if err != nil {
		return models.AgentMeta{}, err
	}

	if err := as.dc.StartContainer(ctx, containerID); err != nil {
		return models.AgentMeta{}, err
	}

	return models.AgentMeta{
		Server:  as.serverURL,
		AgentID: configOptions.AgentID.String(),
	}, nil
}
