package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/yourorg/agent-svc/internal/api"
	"github.com/yourorg/agent-svc/internal/config"
	"github.com/yourorg/agent-svc/internal/docker"
	"github.com/yourorg/agent-svc/internal/logger"
	"github.com/yourorg/agent-svc/internal/metrics"
	"github.com/yourorg/agent-svc/internal/service"
	"github.com/yourorg/agent-svc/internal/store"
)

func main() {
	cfg := config.Load()
	l, _ := logger.New()
	defer l.Sync()

	metrics.Register()

	// используется дефолтная реализация DockerClient (реальный Docker)
	dc := docker.New()
	st := store.NewMemoryStore()

	reg := docker.AuthConfig{Server: cfg.RegistryServer, Username: cfg.RegistryUsername, Password: cfg.RegistryPassword}
	svc := service.NewAgentService(dc, st, reg, cfg.DefaultTimeout)
	h := api.NewHandlers(svc)
	router := api.NewRouter(h)

	addr := fmt.Sprintf(":%s", cfg.HTTPPort)
	l.Sugar().Infof("starting server on %s", addr)
	log.Fatal(http.ListenAndServe(addr, router))
}
