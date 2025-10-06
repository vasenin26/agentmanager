package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vasenin26/agentmanager/internal/docker"
	"github.com/vasenin26/agentmanager/internal/external"
	"github.com/vasenin26/agentmanager/internal/metrics"
	"github.com/vasenin26/agentmanager/internal/models"
	"github.com/vasenin26/agentmanager/internal/ssh"
	"github.com/vasenin26/agentmanager/internal/storage"
	"go.uber.org/zap"
)

type OrchestratorService struct {
	dockerClient      docker.DockerClient
	taskClient        *external.TaskClient
	memoryService     *MemoryService
	contextService    *ContextService
	queueService      *QueueService
	agentStateStorage *storage.AgentStateStorage
	agentService      *AgentService
	sshStorage        *ssh.Storage // SSH storage для управления ключами проектов
	agentAPIToken     string       // Общий токен для всех агентов
	logger            *zap.Logger

	// Управление жизненным циклом
	stopChan     chan struct{}
	wg           sync.WaitGroup
	pollInterval time.Duration
	mu           sync.Mutex
}

func NewOrchestratorService(
	dockerClient docker.DockerClient,
	taskClient *external.TaskClient,
	memoryService *MemoryService,
	contextService *ContextService,
	queueService *QueueService,
	agentStateStorage *storage.AgentStateStorage,
	agentService *AgentService,
	sshStorage *ssh.Storage,
	agentAPIToken string,
	pollInterval time.Duration,
	logger *zap.Logger,
) *OrchestratorService {
	return &OrchestratorService{
		dockerClient:      dockerClient,
		taskClient:        taskClient,
		memoryService:     memoryService,
		contextService:    contextService,
		queueService:      queueService,
		agentStateStorage: agentStateStorage,
		agentService:      agentService,
		sshStorage:        sshStorage,
		agentAPIToken:     agentAPIToken,
		logger:            logger,
		stopChan:          make(chan struct{}),
		pollInterval:      pollInterval,
	}
}

// Start запускает оркестратор
func (os *OrchestratorService) Start(ctx context.Context) {
	os.logger.Info("Starting orchestrator service")

	// Запустить прослушивание Docker событий
	os.wg.Add(1)
	go os.listenDockerEvents(ctx)

	// Запустить цикл запроса задач
	os.wg.Add(1)
	go os.taskPullLoop(ctx)

	os.logger.Info("Orchestrator service started")
}

// Stop останавливает оркестратор
func (os *OrchestratorService) Stop() {
	os.logger.Info("Stopping orchestrator service")
	close(os.stopChan)
	os.wg.Wait()
	os.logger.Info("Orchestrator service stopped")
}

// taskPullLoop цикл запроса задач из внешнего API
func (os *OrchestratorService) taskPullLoop(ctx context.Context) {
	defer os.wg.Done()

	ticker := time.NewTicker(os.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-os.stopChan:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Проверить наличие доступной памяти
			hasMemory, err := os.memoryService.HasAvailableMemory(ctx)
			if err != nil {
				os.logger.Error("Failed to check memory availability", zap.Error(err))
				continue
			}

			if !hasMemory {
				os.logger.Debug("No available memory, waiting...")
				continue
			}

			// Запросить задачу из внешнего API
			task, err := os.taskClient.FetchTask(ctx)
			if err != nil {
				os.logger.Error("Failed to fetch task", zap.Error(err))
				continue
			}

			if task == nil {
				os.logger.Debug("No tasks available")
				continue
			}

			// Обработать полученную задачу
			metrics.TasksFetchedTotal.Inc()
			if err := os.processTask(ctx, task); err != nil {
				os.logger.Error("Failed to process task", zap.String("taskID", task.ID), zap.Error(err))
				metrics.TasksFailedTotal.Inc()
			}
		}
	}
}

// processTask обработка полученной задачи
func (os *OrchestratorService) processTask(ctx context.Context, task *models.TaskDTO) error {
	os.logger.Info("Processing task", zap.String("taskID", task.ID))

	// Проверить требуется ли контекст для задачи
	if task.ContextID == nil {
		// Контекст не требуется - запустить агента без контекста
		os.logger.Info("Task does not require context, starting agent", zap.String("taskID", task.ID))
		return os.startAgentForTask(ctx, task, nil)
	}

	// Контекст требуется
	contextID := *task.ContextID
	os.logger.Info("Task requires context", zap.String("taskID", task.ID), zap.String("contextID", contextID))

	// Получить или создать контекст
	contextDTO, err := os.contextService.GetOrCreateContext(ctx, contextID)
	if err != nil {
		return fmt.Errorf("failed to get or create context: %w", err)
	}

	// Проверить доступен ли контекст
	if contextDTO.IsOccupied {
		// Контекст занят - поместить задачу в локальную очередь контекста
		os.logger.Info("Context is occupied, enqueuing task", zap.String("taskID", task.ID), zap.String("contextID", contextID))
		if err := os.queueService.EnqueueTaskForContext(ctx, contextID, task.ID); err != nil {
			return fmt.Errorf("failed to enqueue task: %w", err)
		}

		// Обновить метрику длины очереди
		queueLength, _ := os.queueService.GetQueueLength(ctx, contextID)
		metrics.ContextQueueLength.WithLabelValues(contextID).Set(float64(queueLength))

		return nil
	}

	// Контекст доступен - занять контекст и запустить агента
	return os.startAgentForTask(ctx, task, contextDTO)
}

// validateAndPrepareProjectKey проверяет и подготавливает SSH ключи для проекта
func (os *OrchestratorService) validateAndPrepareProjectKey(ctx context.Context, task *models.TaskDTO) (*ssh.ProjectKeyPair, error) {
	projectID := task.ProjectID

	// Проверяем существование ключей для проекта
	if os.sshStorage.ProjectKeyPairExists(projectID) {
		// Ключи существуют
		keyPair, err := os.sshStorage.GetProjectKeyPair(projectID)
		if err != nil {
			return nil, fmt.Errorf("failed to get project key pair: %w", err)
		}

		// Если публичный ключ передан в задаче, проверяем его
		if task.PublicKey != nil && *task.PublicKey != "" {
			isValid, err := os.sshStorage.ValidateProjectPublicKey(projectID, *task.PublicKey)
			if err != nil {
				os.logger.Error("Failed to validate public key",
					zap.String("projectID", projectID),
					zap.Error(err))
				return nil, fmt.Errorf("failed to validate public key: %w", err)
			}

			if !isValid {
				os.logger.Warn("Public key mismatch, generating new key pair",
					zap.String("projectID", projectID))
				// Публичный ключ не совпадает - генерируем новую пару
				return os.generateAndRegisterProjectKey(ctx, projectID)
			}
		}

		// Ключи валидны
		return keyPair, nil
	}

	// Ключей нет - генерируем новую пару
	os.logger.Info("Project keys not found, generating new key pair",
		zap.String("projectID", projectID))
	return os.generateAndRegisterProjectKey(ctx, projectID)
}

// generateAndRegisterProjectKey генерирует новую пару ключей и отправляет публичный ключ в API
func (os *OrchestratorService) generateAndRegisterProjectKey(ctx context.Context, projectID string) (*ssh.ProjectKeyPair, error) {
	// Генерируем новую пару ключей
	keyPair, err := os.sshStorage.GenerateAndStoreProjectKeyPair(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate project key pair: %w", err)
	}

	// Отправляем публичный ключ в API
	if err := os.taskClient.UpdateProjectPublicKey(ctx, projectID, keyPair.PublicKey); err != nil {
		os.logger.Error("Failed to update project public key in API",
			zap.String("projectID", projectID),
			zap.Error(err))
		// Не критично - ключи уже сохранены локально, продолжаем
	} else {
		os.logger.Info("Successfully registered project public key",
			zap.String("projectID", projectID))
	}

	return keyPair, nil
}

// startAgentForTask запуск агента для задачи
func (os *OrchestratorService) startAgentForTask(ctx context.Context, task *models.TaskDTO, contextDTO *models.ContextDTO) error {
	os.mu.Lock()
	defer os.mu.Unlock()

	agentID := uuid.New().String()
	os.logger.Info("Starting agent for task",
		zap.String("agentID", agentID),
		zap.String("taskID", task.ID),
		zap.String("projectID", task.ProjectID))

	// Проверить и подготовить SSH ключи проекта
	projectKeys, err := os.validateAndPrepareProjectKey(ctx, task)
	if err != nil {
		return fmt.Errorf("failed to validate project keys: %w", err)
	}

	// Подготовить параметры для агента
	var contextVolumeID *string
	if contextDTO != nil {
		contextVolumeID = &contextDTO.VolumeID
		// Занять контекст
		if err := os.contextService.OccupyContext(ctx, contextDTO.ID, agentID); err != nil {
			return fmt.Errorf("failed to occupy context: %w", err)
		}
	}

	// Получить лимит памяти
	memoryLimit := os.memoryService.agentMemoryLimit

	// Создать ConfigOptions для агента
	configOptions := models.ConfigOptions{
		AgentID: uuid.MustParse(agentID),
		Token:   os.agentAPIToken, // Общий токен из конфигурации оркестратора
	}

	// Запустить агента с приватным ключом проекта
	agentMeta, err := os.agentService.StartAgentForTask(
		configOptions,
		task.ID,
		contextVolumeID,
		memoryLimit,
		projectKeys.PrivateKey, // Передаем приватный ключ проекта
	)
	if err != nil {
		// Если не удалось запустить агента, освободить контекст
		if contextDTO != nil {
			os.contextService.ReleaseContext(ctx, contextDTO.ID)
		}
		return fmt.Errorf("failed to start agent: %w", err)
	}

	// Получить containerID - в реальной реализации нужно будет расширить AgentMeta или получить containerID другим способом
	// Для упрощения, будем получать containerID из Docker
	containers, err := os.dockerClient.ListRunnedContainers(ctx)
	if err != nil {
		os.logger.Error("Failed to list containers", zap.Error(err))
		return fmt.Errorf("failed to list containers: %w", err)
	}

	// Найти последний созданный контейнер для агента
	var containerID string
	for _, container := range containers {
		if container.State == "running" {
			containerID = container.ID
			break
		}
	}

	if containerID == "" {
		os.logger.Error("Failed to find container for agent", zap.String("agentID", agentID))
		if contextDTO != nil {
			os.contextService.ReleaseContext(ctx, contextDTO.ID)
		}
		return fmt.Errorf("failed to find container for agent")
	}

	// Сохранить состояние агента в БД
	var contextIDPtr *string
	if contextDTO != nil {
		contextIDPtr = &contextDTO.ID
	}

	agentState := &models.AgentStateDTO{
		AgentID:     agentMeta.AgentID,
		ContainerID: containerID,
		TaskID:      task.ID,
		ContextID:   contextIDPtr,
		StartedAt:   time.Now(),
		MemoryLimit: memoryLimit,
	}

	if err := os.agentStateStorage.CreateAgentState(ctx, agentState); err != nil {
		os.logger.Error("Failed to save agent state", zap.Error(err))
		// Продолжаем выполнение, так как агент уже запущен
	}

	// Обновить метрики
	metrics.TasksProcessedTotal.Inc()
	metrics.ActiveAgents.Inc()
	if contextDTO != nil {
		metrics.ContextsActive.Inc()
	}

	os.logger.Info("Successfully started agent for task",
		zap.String("agentID", agentID),
		zap.String("containerID", containerID),
		zap.String("taskID", task.ID))

	return nil
}

// handleAgentCompletion обработка завершения агента
func (os *OrchestratorService) handleAgentCompletion(ctx context.Context, containerID string) error {
	os.mu.Lock()
	defer os.mu.Unlock()

	os.logger.Info("Handling agent completion", zap.String("containerID", containerID))

	// Получить состояние агента из БД
	agentState, err := os.agentStateStorage.GetAgentStateByContainerID(ctx, containerID)
	if err != nil {
		os.logger.Error("Failed to get agent state", zap.String("containerID", containerID), zap.Error(err))
		return err
	}

	// Обновить метрики
	metrics.ActiveAgents.Dec()

	// Если агент работал с контекстом
	if agentState.ContextID != nil {
		contextID := *agentState.ContextID

		// Проверить наличие задач в очереди контекста
		hasTasks, err := os.queueService.HasPendingTasks(ctx, contextID)
		if err != nil {
			os.logger.Error("Failed to check pending tasks", zap.String("contextID", contextID), zap.Error(err))
		}

		if hasTasks {
			// Есть задачи - запустить агента для следующей задачи
			os.logger.Info("Context has pending tasks, starting next task", zap.String("contextID", contextID))

			queueItem, err := os.queueService.GetNextTaskForContext(ctx, contextID)
			if err != nil {
				os.logger.Error("Failed to get next task", zap.String("contextID", contextID), zap.Error(err))
			} else {
				// Создать TaskDTO для следующей задачи
				nextTask := &models.TaskDTO{
					ID:        queueItem.TaskID,
					ContextID: &contextID,
					Timeout:   0, // Timeout будет определен внешней системой
				}

				// Получить контекст
				contextDTO, err := os.contextService.GetOrCreateContext(ctx, contextID)
				if err != nil {
					os.logger.Error("Failed to get context", zap.String("contextID", contextID), zap.Error(err))
				} else {
					// Освободить контекст перед запуском нового агента
					os.contextService.ReleaseContext(ctx, contextID)

					// Запустить агента для следующей задачи
					go os.startAgentForTask(context.Background(), nextTask, contextDTO)
				}

				// Обновить метрику длины очереди
				queueLength, _ := os.queueService.GetQueueLength(ctx, contextID)
				metrics.ContextQueueLength.WithLabelValues(contextID).Set(float64(queueLength))
			}
		} else {
			// Задач нет - освободить контекст
			os.logger.Info("No pending tasks, releasing context", zap.String("contextID", contextID))
			if err := os.contextService.ReleaseContext(ctx, contextID); err != nil {
				os.logger.Error("Failed to release context", zap.String("contextID", contextID), zap.Error(err))
			}
			metrics.ContextsActive.Dec()
		}
	}

	// Удалить состояние агента из БД
	if err := os.agentStateStorage.DeleteAgentState(ctx, agentState.AgentID); err != nil {
		os.logger.Error("Failed to delete agent state", zap.String("agentID", agentState.AgentID), zap.Error(err))
	}

	// Отметить задачу как выполненную в внешнем API
	if err := os.taskClient.MarkTaskCompleted(ctx, agentState.TaskID); err != nil {
		os.logger.Error("Failed to mark task as completed", zap.String("taskID", agentState.TaskID), zap.Error(err))
	}

	os.logger.Info("Successfully handled agent completion", zap.String("containerID", containerID))
	return nil
}

// handleAgentFailure обработка сбоя агента
func (os *OrchestratorService) handleAgentFailure(ctx context.Context, containerID string) error {
	os.mu.Lock()
	defer os.mu.Unlock()

	os.logger.Warn("Handling agent failure", zap.String("containerID", containerID))

	// Получить состояние агента из БД
	agentState, err := os.agentStateStorage.GetAgentStateByContainerID(ctx, containerID)
	if err != nil {
		os.logger.Error("Failed to get agent state", zap.String("containerID", containerID), zap.Error(err))
		return err
	}

	// Обновить метрики
	metrics.ActiveAgents.Dec()
	metrics.TasksFailedTotal.Inc()

	// Освободить контекст (если был привязан)
	if agentState.ContextID != nil {
		contextID := *agentState.ContextID
		if err := os.contextService.ReleaseContext(ctx, contextID); err != nil {
			os.logger.Error("Failed to release context", zap.String("contextID", contextID), zap.Error(err))
		}
		metrics.ContextsActive.Dec()
	}

	// Удалить состояние агента из БД
	if err := os.agentStateStorage.DeleteAgentState(ctx, agentState.AgentID); err != nil {
		os.logger.Error("Failed to delete agent state", zap.String("agentID", agentState.AgentID), zap.Error(err))
	}

	os.logger.Info("Successfully handled agent failure", zap.String("containerID", containerID))
	return nil
}

// listenDockerEvents прослушивание событий Docker
func (os *OrchestratorService) listenDockerEvents(ctx context.Context) {
	defer os.wg.Done()

	eventChan := make(chan docker.DockerEvent, 100)

	if err := os.dockerClient.ListenEvents(ctx, eventChan); err != nil {
		os.logger.Error("Failed to listen to Docker events", zap.Error(err))
		return
	}

	for {
		select {
		case <-os.stopChan:
			return
		case <-ctx.Done():
			return
		case event := <-eventChan:
			os.logger.Debug("Received Docker event",
				zap.String("containerID", event.ContainerID),
				zap.String("status", event.Status),
				zap.Int("exitCode", event.ExitCode))

			// Определить тип завершения (успешное/сбой)
			if event.Status == "die" || event.Status == "stop" {
				if event.ExitCode == 0 {
					// Успешное завершение
					go os.handleAgentCompletion(context.Background(), event.ContainerID)
				} else {
					// Сбой
					go os.handleAgentFailure(context.Background(), event.ContainerID)
				}
			}
		}
	}
}
