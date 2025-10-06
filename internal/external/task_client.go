package external

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vasenin26/agentmanager/internal/models"
	"go.uber.org/zap"
)

type TaskClient struct {
	baseURL    string // Base URL с префиксом API, например: https://api.example.com/api/v1/orchestrator
	token      string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewTaskClient создает новый клиент для Task API
// baseURL должен включать префикс API, например: https://api.example.com/api/v1/orchestrator
func NewTaskClient(baseURL, token string, timeout time.Duration, logger *zap.Logger) *TaskClient {
	return &TaskClient{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
	}
}

// FetchTask запрашивает следующую задачу из внешнего API
func (c *TaskClient) FetchTask(ctx context.Context) (*models.TaskDTO, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/tasks/next", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		// Нет доступных задач
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var task models.TaskDTO
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, err
	}

	c.logger.Info("Fetched task from external API", zap.String("taskID", task.ID))
	return &task, nil
}

// ReserveTask резервирует задачу с указанием времени до запуска агента и UUID воркера
func (c *TaskClient) ReserveTask(ctx context.Context, taskID string, reserveSeconds int, agentUUID string) error {
	payload := map[string]interface{}{
		"reserve_seconds": reserveSeconds,
		"agent_uuid":      agentUUID,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/tasks/"+taskID+"/reserve", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		// Обработать конфликт резервирования
		var conflictResp struct {
			Error         string `json:"error"`
			ReservedBy    string `json:"reserved_by,omitempty"`
			ReservedUntil string `json:"reserved_until,omitempty"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&conflictResp); err == nil {
			c.logger.Warn("Task reservation conflict",
				zap.String("taskID", taskID),
				zap.String("error", conflictResp.Error),
				zap.String("reserved_until", conflictResp.ReservedUntil))
		}
		return fmt.Errorf("task already reserved: %s", conflictResp.Error)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	c.logger.Info("Reserved task",
		zap.String("taskID", taskID),
		zap.String("agentUUID", agentUUID),
		zap.Int("reserveSeconds", reserveSeconds))
	return nil
}

// UpdateProjectPublicKey отправляет публичный ключ проекта обратно в API
func (c *TaskClient) UpdateProjectPublicKey(ctx context.Context, projectID, publicKey string) error {
	payload := map[string]string{
		"public_key": publicKey,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", c.baseURL+"/projects/"+projectID+"/key", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	c.logger.Info("Updated project public key", zap.String("projectID", projectID))
	return nil
}
