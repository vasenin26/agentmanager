package store

import (
	"sync"

	"github.com/yourorg/agent-svc/internal/models"
)

type MemoryStore struct {
	mu sync.RWMutex
	items map[string]models.AgentInfo
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{items: make(map[string]models.AgentInfo)} }

func (s *MemoryStore) Add(a models.AgentInfo) {
	s.mu.Lock(); defer s.mu.Unlock(); s.items[a.ID] = a
}
func (s *MemoryStore) Get(id string) (models.AgentInfo, bool) {
	s.mu.RLock(); defer s.mu.RUnlock(); a, ok := s.items[id]; return a, ok
}
func (s *MemoryStore) List() []models.AgentInfo {
	s.mu.RLock(); defer s.mu.RUnlock(); res := make([]models.AgentInfo, 0, len(s.items)); for _, v := range s.items { res = append(res, v) }; return res
}
func (s *MemoryStore) Update(a models.AgentInfo) { s.mu.Lock(); defer s.mu.Unlock(); s.items[a.ID] = a }
func (s *MemoryStore) Remove(id string) { s.mu.Lock(); defer s.mu.Unlock(); delete(s.items, id) }
