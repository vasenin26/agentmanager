package models

// TaskDTO - задача из внешнего API
type TaskDTO struct {
	ID        string  `json:"id"`         // ID задачи
	ContextID *string `json:"context_id"` // ID контекста (nil если контекст не требуется)
	Timeout   int     `json:"timeout"`    // Таймаут выполнения в секундах
	ProjectID string  `json:"project_id"` // ID проекта к которому привязана задача
	PublicKey *string `json:"public_key"` // Публичный SSH ключ проекта (может быть nil)
}
