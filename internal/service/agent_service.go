package service

import (
	"context"
	"os"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
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

// StartAgentForTask запускает агента для конкретной задачи с опциональным контекстом
func (as *AgentService) StartAgentForTask(
	configOptions models.ConfigOptions,
	taskID string,
	contextVolumeID *string,
	memoryLimit int64,
	projectPrivateKey string, // Приватный ключ проекта
) (models.AgentMeta, error) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, as.defaultTimeout)
	defer cancel()

	// SSH ключи (существующая логика)
	sshKeyPair, err := as.sshStorage.GetKeyPair(configOptions.AgentID.String())
	if err != nil {
		sshKeyPair, err = as.sshStorage.GenerateAndStoreKeyPair(configOptions.AgentID.String())
		if err != nil {
			return models.AgentMeta{}, err
		}
	}

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
			"AGENT_ID":                configOptions.AgentID.String(),
			"API_TOKEN":               configOptions.Token,
			"TASK_ID":                 taskID,                // Новая переменная
			"SSH_PRIVATE_KEY":         sshKeyPair.PrivateKey, // SSH ключ агента (для Git операций агента)
			"PROJECT_SSH_PRIVATE_KEY": projectPrivateKey,     // SSH ключ проекта (для клонирования репозитория)
			"API_HOST":                as.apiHost,
			"OPENAI_MODEL":            as.openaiModel,
			"OPENAI_API_KEY":          as.openaiApiKey,
			"GIT_USER_NAME":           as.gitUserName,
			"GIT_USER_EMAIL":          as.gitUserEmail,
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
		Server:    as.serverURL,
		AgentID:   configOptions.AgentID.String(),
		PublicKey: sshKeyPair.PublicKey,
	}, nil
}
