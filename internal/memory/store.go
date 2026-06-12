// Package memory manages session history read/write and long-term memory storage.
// Short-term history is stored in the messages table; long-term memory is stored
// in the memories table (with optional vector embeddings).
//
// Actual SQL and model definitions are delegated to internal/store/repo;
// this package provides semantic wrapping and forwarding.
package memory

import (
	"context"
	"database/sql"

	"github.com/zboya/nurvis/internal/store/repo"
)

// Message and Memory models are defined in the repo package; type aliases are
// used here for external compatibility.
type (
	Message = repo.Message
	Memory  = repo.Memory
)

// Store provides memory read/write operations.
type Store struct {
	messages *repo.MessageRepo
	memories *repo.MemoryRepo
}

// New creates a new Memory Store.
func New(db *sql.DB) *Store {
	return &Store{
		messages: repo.NewMessageRepo(db),
		memories: repo.NewMemoryRepo(db),
	}
}

// SaveMessage persists a single message (INSERT OR IGNORE to prevent duplicates).
func (s *Store) SaveMessage(ctx context.Context, m Message) error {
	return s.messages.Save(ctx, m)
}

// GetMessages retrieves session history (most recent limit messages, in ascending order).
func (s *Store) GetMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	return s.messages.List(ctx, sessionID, limit)
}

// SaveMemory writes a long-term memory entry.
func (s *Store) SaveMemory(ctx context.Context, m Memory) error {
	return s.memories.Save(ctx, m)
}

// SearchMemories retrieves memories filtered by agent and scope.
func (s *Store) SearchMemories(ctx context.Context, agentID, scope string, limit int) ([]Memory, error) {
	return s.memories.Search(ctx, agentID, scope, limit)
}
