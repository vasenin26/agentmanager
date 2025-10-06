package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vasenin26/agentmanager/internal/models"
	"go.etcd.io/bbolt"
)

type ContextStorage struct {
	db *bbolt.DB
}

func NewContextStorage(db *bbolt.DB) *ContextStorage {
	return &ContextStorage{db: db}
}

func (s *ContextStorage) CreateContext(ctx context.Context, contextDTO *models.ContextDTO) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("contexts"))
		data, err := json.Marshal(contextDTO)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(contextDTO.ID), data)
	})
}

func (s *ContextStorage) GetContext(ctx context.Context, contextID string) (*models.ContextDTO, error) {
	var result *models.ContextDTO
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("contexts"))
		data := bucket.Get([]byte(contextID))
		if data == nil {
			return fmt.Errorf("context not found: %s", contextID)
		}
		result = &models.ContextDTO{}
		return json.Unmarshal(data, result)
	})
	return result, err
}

func (s *ContextStorage) OccupyContext(ctx context.Context, contextID, agentID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("contexts"))
		data := bucket.Get([]byte(contextID))
		if data == nil {
			return fmt.Errorf("context not found: %s", contextID)
		}

		var contextDTO models.ContextDTO
		if err := json.Unmarshal(data, &contextDTO); err != nil {
			return err
		}

		now := time.Now()
		contextDTO.IsOccupied = true
		contextDTO.AgentID = &agentID
		contextDTO.OccupiedAt = &now

		newData, err := json.Marshal(&contextDTO)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(contextID), newData)
	})
}

func (s *ContextStorage) ReleaseContext(ctx context.Context, contextID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("contexts"))
		data := bucket.Get([]byte(contextID))
		if data == nil {
			return fmt.Errorf("context not found: %s", contextID)
		}

		var contextDTO models.ContextDTO
		if err := json.Unmarshal(data, &contextDTO); err != nil {
			return err
		}

		contextDTO.IsOccupied = false
		contextDTO.AgentID = nil
		contextDTO.OccupiedAt = nil

		newData, err := json.Marshal(&contextDTO)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(contextID), newData)
	})
}

func (s *ContextStorage) DeleteContext(ctx context.Context, contextID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("contexts"))
		return bucket.Delete([]byte(contextID))
	})
}

func (s *ContextStorage) IsContextOccupied(ctx context.Context, contextID string) (bool, error) {
	contextDTO, err := s.GetContext(ctx, contextID)
	if err != nil {
		return false, err
	}
	return contextDTO.IsOccupied, nil
}

func (s *ContextStorage) ListAllContexts(ctx context.Context) ([]*models.ContextDTO, error) {
	var result []*models.ContextDTO
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("contexts"))
		cursor := bucket.Cursor()

		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			var contextDTO models.ContextDTO
			if err := json.Unmarshal(v, &contextDTO); err != nil {
				continue
			}
			result = append(result, &contextDTO)
		}
		return nil
	})
	return result, err
}
