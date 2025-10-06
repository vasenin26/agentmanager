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
	baseURL    string
	token      string
	httpClient *http.Client
	logger     *zap.Logger
}

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

// MarkTaskCompleted отмечает задачу как выполненную
func (c *TaskClient) MarkTaskCompleted(ctx context.Context, taskID string) error {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/tasks/"+taskID+"/complete", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	c.logger.Info("Marked task as completed", zap.String("taskID", taskID))
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
