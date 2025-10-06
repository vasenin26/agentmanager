package service

import (
	"context"

	"github.com/vasenin26/agentmanager/internal/docker"
	"github.com/vasenin26/agentmanager/internal/models"
	"github.com/vasenin26/agentmanager/internal/storage"
	"go.uber.org/zap"
)

type MemoryService struct {
	dockerClient      docker.DockerClient
	agentStateStorage *storage.AgentStateStorage
	agentMemoryLimit  int64
	logger            *zap.Logger
}

func NewMemoryService(
	dockerClient docker.DockerClient,
	agentStateStorage *storage.AgentStateStorage,
	agentMemoryLimitMB int64,
	logger *zap.Logger,
) *MemoryService {
	return &MemoryService{
		dockerClient:      dockerClient,
		agentStateStorage: agentStateStorage,
		agentMemoryLimit:  agentMemoryLimitMB * 1024 * 1024,
		logger:            logger,
	}
}

// GetMemoryStatus получает статус памяти
func (ms *MemoryService) GetMemoryStatus(ctx context.Context) (*models.MemoryStatusDTO, error) {
	totalMemory, err := ms.dockerClient.GetSystemMemory(ctx)
	if err != nil {
		return nil, err
	}

	usedMemory, err := ms.CalculateUsedMemory(ctx)
	if err != nil {
		return nil, err
	}

	availableMemory := totalMemory - usedMemory

	return &models.MemoryStatusDTO{
		TotalMemory:      totalMemory,
		UsedMemory:       usedMemory,
		AvailableMemory:  availableMemory,
		AgentMemoryLimit: ms.agentMemoryLimit,
	}, nil
}

// HasAvailableMemory проверяет доступность памяти для нового агента
func (ms *MemoryService) HasAvailableMemory(ctx context.Context) (bool, error) {
	status, err := ms.GetMemoryStatus(ctx)
	if err != nil {
		return false, err
	}

	return status.AvailableMemory >= ms.agentMemoryLimit, nil
}

// CalculateUsedMemory рассчитывает используемую агентами память
func (ms *MemoryService) CalculateUsedMemory(ctx context.Context) (int64, error) {
	agents, err := ms.agentStateStorage.ListActiveAgents(ctx)
	if err != nil {
		return 0, err
	}

	var totalUsed int64
	for _, agent := range agents {
		totalUsed += agent.MemoryLimit
	}

	return totalUsed, nil
}

