package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vasenin26/agentmanager/internal/models"
	"go.etcd.io/bbolt"
)

type AgentStateStorage struct {
	db *bbolt.DB
}

func NewAgentStateStorage(db *bbolt.DB) *AgentStateStorage {
	return &AgentStateStorage{db: db}
}

func (s *AgentStateStorage) CreateAgentState(ctx context.Context, state *models.AgentStateDTO) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("agent_states"))
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(state.AgentID), data)
	})
}

func (s *AgentStateStorage) GetAgentState(ctx context.Context, agentID string) (*models.AgentStateDTO, error) {
	var result *models.AgentStateDTO
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("agent_states"))
		data := bucket.Get([]byte(agentID))
		if data == nil {
			return fmt.Errorf("agent state not found: %s", agentID)
		}
		result = &models.AgentStateDTO{}
		return json.Unmarshal(data, result)
	})
	return result, err
}

func (s *AgentStateStorage) GetAgentStateByContainerID(ctx context.Context, containerID string) (*models.AgentStateDTO, error) {
	var result *models.AgentStateDTO
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("agent_states"))
		cursor := bucket.Cursor()

		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			var state models.AgentStateDTO
			if err := json.Unmarshal(v, &state); err != nil {
				continue
			}
			if state.ContainerID == containerID {
				result = &state
				return nil
			}
		}
		return fmt.Errorf("agent state not found for container: %s", containerID)
	})
	return result, err
}

func (s *AgentStateStorage) DeleteAgentState(ctx context.Context, agentID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("agent_states"))
		return bucket.Delete([]byte(agentID))
	})
}

func (s *AgentStateStorage) ListActiveAgents(ctx context.Context) ([]*models.AgentStateDTO, error) {
	var result []*models.AgentStateDTO
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("agent_states"))
		cursor := bucket.Cursor()

		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			var state models.AgentStateDTO
			if err := json.Unmarshal(v, &state); err != nil {
				continue
			}
			result = append(result, &state)
		}
		return nil
	})
	return result, err
}

func (s *AgentStateStorage) GetAgentByContextID(ctx context.Context, contextID string) (*models.AgentStateDTO, error) {
	var result *models.AgentStateDTO
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("agent_states"))
		cursor := bucket.Cursor()

		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			var state models.AgentStateDTO
			if err := json.Unmarshal(v, &state); err != nil {
				continue
			}
			if state.ContextID != nil && *state.ContextID == contextID {
				result = &state
				return nil
			}
		}
		return fmt.Errorf("agent state not found for context: %s", contextID)
	})
	return result, err
}

