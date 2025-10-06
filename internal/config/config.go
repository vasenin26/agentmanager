package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPPort         string
	DockerHost       string
	RegistryServer   string
	RegistryUsername string
	RegistryPassword string
	DefaultTimeout   time.Duration
	// SSH keys storage
	SSHKeysDir string
	// Agent configuration
	APIHost      string
	OpenAIModel  string
	OpenAIAPIKey string
	GitUserName  string
	GitUserEmail string

	// Bolt Database
	BoltDBPath string

	// External Task API
	TaskAPIURL       string
	TaskAPIToken     string
	TaskAPITimeout   time.Duration
	TaskPollInterval time.Duration

	// Agent Memory
	AgentMemoryLimitMB int64

	// Orchestrator (всегда включен)
	AgentAPIToken string // Общий токен для всех агентов
}

func Load() Config {
	p := os.Getenv("HTTP_PORT")
	if p == "" {
		p = "8080"
	}

	sshKeysDir := os.Getenv("SSH_KEYS_DIR")
	if sshKeysDir == "" {
		sshKeysDir = "./keys"
	}

	boltDBPath := os.Getenv("BOLT_DB_PATH")
	if boltDBPath == "" {
		boltDBPath = "./data/orchestrator.db"
	}

	taskAPITimeout := 10 * time.Second
	if v := os.Getenv("TASK_API_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			taskAPITimeout = d
		}
	}

	taskPollInterval := 5 * time.Second
	if v := os.Getenv("TASK_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			taskPollInterval = d
		}
	}

	agentMemoryLimitMB := int64(512)
	if v := os.Getenv("AGENT_MEMORY_LIMIT_MB"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			agentMemoryLimitMB = i
		}
	}

	agentAPIToken := os.Getenv("AGENT_API_TOKEN")
	if agentAPIToken == "" {
		agentAPIToken = "default-agent-token" // Значение по умолчанию
	}

	return Config{
		HTTPPort:         p,
		DockerHost:       os.Getenv("DOCKER_HOST"),
		RegistryServer:   os.Getenv("REGISTRY_SERVER"),
		RegistryUsername: os.Getenv("REGISTRY_USERNAME"),
		RegistryPassword: os.Getenv("REGISTRY_PASSWORD"),
		DefaultTimeout:   30 * time.Second,
		// SSH keys storage
		SSHKeysDir: sshKeysDir,
		// Agent configuration
		APIHost:      os.Getenv("API_HOST"),
		OpenAIModel:  os.Getenv("OPENAI_MODEL"),
		OpenAIAPIKey: os.Getenv("OPENAI_API_KEY"),
		GitUserName:  os.Getenv("GIT_USER_NAME"),
		GitUserEmail: os.Getenv("GIT_USER_EMAIL"),
		// Orchestrator configuration
		BoltDBPath:         boltDBPath,
		TaskAPIURL:         os.Getenv("TASK_API_URL"),
		TaskAPIToken:       os.Getenv("TASK_API_TOKEN"),
		TaskAPITimeout:     taskAPITimeout,
		TaskPollInterval:   taskPollInterval,
		AgentMemoryLimitMB: agentMemoryLimitMB,
		AgentAPIToken:      agentAPIToken,
	}
}
