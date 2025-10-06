package models

import "time"

// AgentStateDTO - состояние работающего агента
type AgentStateDTO struct {
	AgentID     string    `json:"agent_id"`
	ContainerID string    `json:"container_id"`
	TaskID      string    `json:"task_id"`
	ContextID   *string   `json:"context_id"` // nil если агент без контекста
	StartedAt   time.Time `json:"started_at"`
	MemoryLimit int64     `json:"memory_limit"` // Лимит памяти в байтах
}
