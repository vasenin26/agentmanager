package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	promhttp "github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/vasenin26/agentmanager/internal/config"
	"github.com/vasenin26/agentmanager/internal/docker"
	"github.com/vasenin26/agentmanager/internal/external"
	"github.com/vasenin26/agentmanager/internal/logger"
	"github.com/vasenin26/agentmanager/internal/metrics"
	"github.com/vasenin26/agentmanager/internal/service"
	"github.com/vasenin26/agentmanager/internal/ssh"
	"github.com/vasenin26/agentmanager/internal/storage"
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
	agentSvc := service.NewAgentService(dc, reg, cfg.DefaultTimeout, serverURL, sshStorage,
		cfg.APIHost, cfg.OpenAIModel, cfg.OpenAIAPIKey, cfg.GitUserName, cfg.GitUserEmail)

	// Orchestrator (всегда включен)
	var orchestrator *service.OrchestratorService
	if cfg.TaskAPIURL != "" {
		l.Info("Orchestrator is enabled, initializing...")

		// Создать директорию для БД если не существует
		dbDir := filepath.Dir(cfg.BoltDBPath)
		if err := os.MkdirAll(dbDir, 0700); err != nil {
			l.Fatal("Failed to create db directory", zap.Error(err))
		}

		// Открыть bbolt БД
		boltStorage, err := storage.NewBoltStorage(cfg.BoltDBPath)
		if err != nil {
			l.Fatal("Failed to open bolt db", zap.Error(err))
		}
		defer boltStorage.Close()

		// Создать storage слои
		contextStorage := storage.NewContextStorage(boltStorage.DB())
		agentStateStorage := storage.NewAgentStateStorage(boltStorage.DB())
		queueStorage := storage.NewContextQueueStorage(boltStorage.DB())

		// Создать task client
		taskClient := external.NewTaskClient(
			cfg.TaskAPIURL,
			cfg.TaskAPIToken,
			cfg.TaskAPITimeout,
			l,
		)

		// Создать сервисы
		memoryService := service.NewMemoryService(
			dc,
			agentStateStorage,
			cfg.AgentMemoryLimitMB,
			l,
		)

		contextService := service.NewContextService(
			dc,
			contextStorage,
			l,
		)

		queueService := service.NewQueueService(
			queueStorage,
			l,
		)

		// Создать оркестратор
		orchestrator = service.NewOrchestratorService(
			dc,
			taskClient,
			memoryService,
			contextService,
			queueService,
			agentStateStorage,
			agentSvc,
			sshStorage, // SSH storage для управления ключами проектов
			cfg.AgentAPIToken,
			cfg.TaskPollInterval,
			l,
		)

		// Запустить оркестратор
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go orchestrator.Start(ctx)
		l.Info("Orchestrator started")
	} else {
		l.Fatal("TASK_API_URL is required")
	}

	// Запустить HTTP сервер только с метриками
	addr := fmt.Sprintf(":%s", cfg.HTTPPort)
	l.Sugar().Infof("starting metrics server on %s", addr)

	// Создаем HTTP mux только с метриками
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// Graceful shutdown
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			l.Fatal("Server failed", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	l.Info("Shutting down server...")

	// Остановить оркестратор
	if orchestrator != nil {
		orchestrator.Stop()
	}

	// Остановить HTTP сервер
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		l.Error("Server forced to shutdown", zap.Error(err))
	}

	l.Info("Server exited")
}
