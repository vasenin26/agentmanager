package service

import (
	"context"

	"github.com/vasenin26/agentmanager/internal/models"
	"github.com/vasenin26/agentmanager/internal/storage"
	"go.uber.org/zap"
)

type QueueService struct {
	queueStorage *storage.ContextQueueStorage
	logger       *zap.Logger
}

func NewQueueService(
	queueStorage *storage.ContextQueueStorage,
	logger *zap.Logger,
) *QueueService {
	return &QueueService{
		queueStorage: queueStorage,
		logger:       logger,
	}
}

// EnqueueTaskForContext добавляет задачу в очередь контекста
func (qs *QueueService) EnqueueTaskForContext(ctx context.Context, contextID, taskID string) error {
	qs.logger.Info("Enqueuing task for context", zap.String("contextID", contextID), zap.String("taskID", taskID))
	return qs.queueStorage.EnqueueTask(ctx, contextID, taskID)
}

// GetNextTaskForContext получает следующую задачу из очереди
func (qs *QueueService) GetNextTaskForContext(ctx context.Context, contextID string) (*models.ContextQueueItem, error) {
	return qs.queueStorage.DequeueTask(ctx, contextID)
}

// HasPendingTasks проверяет наличие задач в очереди
func (qs *QueueService) HasPendingTasks(ctx context.Context, contextID string) (bool, error) {
	return qs.queueStorage.HasQueuedTasks(ctx, contextID)
}

// GetQueueLength получает длину очереди
func (qs *QueueService) GetQueueLength(ctx context.Context, contextID string) (int, error) {
	return qs.queueStorage.GetQueueLength(ctx, contextID)
}

