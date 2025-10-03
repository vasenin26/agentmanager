package config

import (
	"os"
	"time"
)

type Config struct {
	HTTPPort string
	DockerHost string
	RegistryServer string
	RegistryUsername string
	RegistryPassword string
	DefaultTimeout time.Duration
	// SSH keys storage
	SSHKeysDir string
	// Agent configuration
	APIHost      string
	OpenAIModel  string
	OpenAIAPIKey string
	GitUserName  string
	GitUserEmail string
}

func Load() Config {
	p := os.Getenv("HTTP_PORT")
	if p == "" { p = "8080" }
	
	sshKeysDir := os.Getenv("SSH_KEYS_DIR")
	if sshKeysDir == "" { sshKeysDir = "./keys" }
	
	return Config{
		HTTPPort: p,
		DockerHost: os.Getenv("DOCKER_HOST"),
		RegistryServer: os.Getenv("REGISTRY_SERVER"),
		RegistryUsername: os.Getenv("REGISTRY_USERNAME"),
		RegistryPassword: os.Getenv("REGISTRY_PASSWORD"),
		DefaultTimeout: 30 * time.Second,
		// SSH keys storage
		SSHKeysDir: sshKeysDir,
		// Agent configuration
		APIHost:      os.Getenv("API_HOST"),
		OpenAIModel:  os.Getenv("OPENAI_MODEL"),
		OpenAIAPIKey: os.Getenv("OPENAI_API_KEY"),
		GitUserName:  os.Getenv("GIT_USER_NAME"),
		GitUserEmail: os.Getenv("GIT_USER_EMAIL"),
	}
}
