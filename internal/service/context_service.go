package service

import (
	"context"
	"fmt"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
	"github.com/vasenin26/agentmanager/internal/models"
	"github.com/vasenin26/agentmanager/internal/storage"
	"go.uber.org/zap"
)

type ContextService struct {
	dockerClient   docker.DockerClient
	contextStorage *storage.ContextStorage
	logger         *zap.Logger
}

func NewContextService(
	dockerClient docker.DockerClient,
	contextStorage *storage.ContextStorage,
	logger *zap.Logger,
) *ContextService {
	return &ContextService{
		dockerClient:   dockerClient,
		contextStorage: contextStorage,
		logger:         logger,
	}
}

// CreateContext создает новый контекст с Docker volume
func (cs *ContextService) CreateContext(ctx context.Context, contextID string) (*models.ContextDTO, error) {
	cs.logger.Info("Creating context", zap.String("contextID", contextID))

	// Создать Docker volume
	volumeID, err := cs.dockerClient.CreateVolume(ctx, "context-"+contextID)
	if err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	// Создать запись в БД
	contextDTO := &models.ContextDTO{
		ID:         contextID,
		VolumeID:   volumeID,
		IsOccupied: false,
		AgentID:    nil,
		OccupiedAt: nil,
		CreatedAt:  time.Now(),
	}

	if err := cs.contextStorage.CreateContext(ctx, contextDTO); err != nil {
		// Попытаться удалить volume если не удалось создать запись
		cs.dockerClient.DeleteVolume(ctx, volumeID)
		return nil, fmt.Errorf("failed to save context to storage: %w", err)
	}

	cs.logger.Info("Successfully created context", zap.String("contextID", contextID), zap.String("volumeID", volumeID))
	return contextDTO, nil
}

// GetOrCreateContext получает существующий или создает новый контекст
func (cs *ContextService) GetOrCreateContext(ctx context.Context, contextID string) (*models.ContextDTO, error) {
	// Попытаться получить существующий контекст
	contextDTO, err := cs.contextStorage.GetContext(ctx, contextID)
	if err == nil {
		return contextDTO, nil
	}

	// Контекст не найден, создать новый
	return cs.CreateContext(ctx, contextID)
}

// OccupyContext занимает контекст агентом
func (cs *ContextService) OccupyContext(ctx context.Context, contextID, agentID string) error {
	cs.logger.Info("Occupying context", zap.String("contextID", contextID), zap.String("agentID", agentID))
	return cs.contextStorage.OccupyContext(ctx, contextID, agentID)
}

// ReleaseContext освобождает контекст
func (cs *ContextService) ReleaseContext(ctx context.Context, contextID string) error {
	cs.logger.Info("Releasing context", zap.String("contextID", contextID))
	return cs.contextStorage.ReleaseContext(ctx, contextID)
}

// DeleteContext удаляет контекст и связанный volume
func (cs *ContextService) DeleteContext(ctx context.Context, contextID string) error {
	cs.logger.Info("Deleting context", zap.String("contextID", contextID))

	// Получить информацию о контексте
	contextDTO, err := cs.contextStorage.GetContext(ctx, contextID)
	if err != nil {
		return fmt.Errorf("failed to get context: %w", err)
	}

	// Удалить volume
	if err := cs.dockerClient.DeleteVolume(ctx, contextDTO.VolumeID); err != nil {
		cs.logger.Warn("Failed to delete volume", zap.String("volumeID", contextDTO.VolumeID), zap.Error(err))
	}

	// Удалить запись из БД
	if err := cs.contextStorage.DeleteContext(ctx, contextID); err != nil {
		return fmt.Errorf("failed to delete context from storage: %w", err)
	}

	cs.logger.Info("Successfully deleted context", zap.String("contextID", contextID))
	return nil
}

// IsContextAvailable проверяет доступен ли контекст
func (cs *ContextService) IsContextAvailable(ctx context.Context, contextID string) (bool, error) {
	isOccupied, err := cs.contextStorage.IsContextOccupied(ctx, contextID)
	if err != nil {
		return false, err
	}
	return !isOccupied, nil
}
