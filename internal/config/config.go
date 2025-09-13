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
}

func Load() Config {
	p := os.Getenv("HTTP_PORT")
	if p == "" { p = "8080" }
	return Config{
		HTTPPort: p,
		DockerHost: os.Getenv("DOCKER_HOST"),
		RegistryServer: os.Getenv("REGISTRY_SERVER"),
		RegistryUsername: os.Getenv("REGISTRY_USERNAME"),
		RegistryPassword: os.Getenv("REGISTRY_PASSWORD"),
		DefaultTimeout: 30 * time.Second,
	}
}
