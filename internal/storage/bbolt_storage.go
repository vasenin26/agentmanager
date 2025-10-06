package storage

import (
	"time"

	"go.etcd.io/bbolt"
)

type BoltStorage struct {
	db *bbolt.DB
}

// NewBoltStorage - открыть/создать БД
func NewBoltStorage(dbPath string) (*BoltStorage, error) {
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{
		Timeout: 1 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	// Создать buckets
	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("contexts"))
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists([]byte("agent_states"))
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists([]byte("context_queues"))
		return err
	})

	return &BoltStorage{db: db}, err
}

// DB - получить доступ к БД
func (s *BoltStorage) DB() *bbolt.DB {
	return s.db
}

// Close - закрыть БД
func (s *BoltStorage) Close() error {
	return s.db.Close()
}
