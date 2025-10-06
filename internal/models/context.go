package models

import "time"

// ContextDTO - информация о контексте
type ContextDTO struct {
	ID         string     `json:"id"`          // ID контекста
	VolumeID   string     `json:"volume_id"`   // ID Docker volume
	IsOccupied bool       `json:"is_occupied"` // Занят ли контекст агентом
	AgentID    *string    `json:"agent_id"`    // ID агента использующего контекст (nil если свободен)
	OccupiedAt *time.Time `json:"occupied_at"` // Время привязки агента к контексту
	CreatedAt  time.Time  `json:"created_at"`
}

// ContextQueueItem - элемент локальной очереди задач для контекста
type ContextQueueItem struct {
	TaskID   string    `json:"task_id"`
	QueuedAt time.Time `json:"queued_at"`
}
