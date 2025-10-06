package models

// MemoryStatusDTO - статус использования памяти
type MemoryStatusDTO struct {
	TotalMemory      int64 `json:"total_memory"`       // Всего памяти на сервере
	UsedMemory       int64 `json:"used_memory"`        // Используемая память
	AvailableMemory  int64 `json:"available_memory"`   // Доступная память
	AgentMemoryLimit int64 `json:"agent_memory_limit"` // Лимит памяти на один агент
}
