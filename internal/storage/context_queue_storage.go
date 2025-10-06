package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vasenin26/agentmanager/internal/models"
	"go.etcd.io/bbolt"
)

type ContextQueueStorage struct {
	db *bbolt.DB
}

func NewContextQueueStorage(db *bbolt.DB) *ContextQueueStorage {
	return &ContextQueueStorage{db: db}
}

func (s *ContextQueueStorage) EnqueueTask(ctx context.Context, contextID, taskID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("context_queues"))

		// Получить текущую очередь
		var queue []models.ContextQueueItem
		data := bucket.Get([]byte(contextID))
		if data != nil {
			if err := json.Unmarshal(data, &queue); err != nil {
				return err
			}
		}

		// Добавить новый элемент
		queue = append(queue, models.ContextQueueItem{
			TaskID:   taskID,
			QueuedAt: time.Now(),
		})

		// Сохранить обновленную очередь
		newData, err := json.Marshal(queue)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(contextID), newData)
	})
}

func (s *ContextQueueStorage) DequeueTask(ctx context.Context, contextID string) (*models.ContextQueueItem, error) {
	var result *models.ContextQueueItem

	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("context_queues"))

		// Получить текущую очередь
		var queue []models.ContextQueueItem
		data := bucket.Get([]byte(contextID))
		if data == nil || len(data) == 0 {
			return fmt.Errorf("queue is empty for context: %s", contextID)
		}

		if err := json.Unmarshal(data, &queue); err != nil {
			return err
		}

		if len(queue) == 0 {
			return fmt.Errorf("queue is empty for context: %s", contextID)
		}

		// Извлечь первый элемент
		result = &queue[0]
		queue = queue[1:]

		// Сохранить обновленную очередь
		if len(queue) == 0 {
			// Удалить ключ если очередь пуста
			return bucket.Delete([]byte(contextID))
		}

		newData, err := json.Marshal(queue)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(contextID), newData)
	})

	return result, err
}

func (s *ContextQueueStorage) GetQueueLength(ctx context.Context, contextID string) (int, error) {
	var length int
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("context_queues"))
		data := bucket.Get([]byte(contextID))
		if data == nil {
			length = 0
			return nil
		}

		var queue []models.ContextQueueItem
		if err := json.Unmarshal(data, &queue); err != nil {
			return err
		}
		length = len(queue)
		return nil
	})
	return length, err
}

func (s *ContextQueueStorage) ClearQueue(ctx context.Context, contextID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("context_queues"))
		return bucket.Delete([]byte(contextID))
	})
}

func (s *ContextQueueStorage) HasQueuedTasks(ctx context.Context, contextID string) (bool, error) {
	length, err := s.GetQueueLength(ctx, contextID)
	if err != nil {
		return false, err
	}
	return length > 0, nil
}

