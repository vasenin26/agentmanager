package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/vasenin26/agentmanager/internal/api"
	"github.com/vasenin26/agentmanager/internal/config"
	"github.com/vasenin26/agentmanager/internal/docker"
	"github.com/vasenin26/agentmanager/internal/logger"
	"github.com/vasenin26/agentmanager/internal/metrics"
	"github.com/vasenin26/agentmanager/internal/service"
	"github.com/vasenin26/agentmanager/internal/ssh"
	"go.uber.org/zap"
)

func main() {
	cfg := config.Load()
	l, _ := logger.New()
	defer l.Sync()

	// Создаем реальную реализацию DockerClient
	dc, err := docker.New(l)
	if err != nil {
		l.Fatal("Failed to create docker client", zap.Error(err))
	}

	// Регистрируем метрики с Docker клиентом
	metrics.RegisterWithDockerClient(dc)
	defer func() {
		if err := dc.Close(); err != nil {
			l.Error("Failed to close docker client", zap.Error(err))
		}
	}()

	// Create SSH storage
	sshStorage, err := ssh.NewStorage(cfg.SSHKeysDir)
	if err != nil {
		l.Fatal("Failed to create SSH storage", zap.Error(err))
	}

	reg := docker.AuthConfig{Server: cfg.RegistryServer, Username: cfg.RegistryUsername, Password: cfg.RegistryPassword}
	serverURL := fmt.Sprintf("http://localhost:%s", cfg.HTTPPort)
	svc := service.NewAgentService(dc, reg, cfg.DefaultTimeout, serverURL, sshStorage,
		cfg.APIHost, cfg.OpenAIModel, cfg.OpenAIAPIKey, cfg.GitUserName, cfg.GitUserEmail)
	h := api.NewHandlers(svc)
	router := api.NewRouter(h)

	addr := fmt.Sprintf(":%s", cfg.HTTPPort)
	l.Sugar().Infof("starting server on %s", addr)
	log.Fatal(http.ListenAndServe(addr, router))
}
