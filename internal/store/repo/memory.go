package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Memory represents a row in the memories table.
type Memory struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id,omitempty"`
	Scope     string    `json:"scope"` // global | agent | session
	SessionID string    `json:"session_id,omitempty"`
	Kind      string    `json:"kind"` // preference | fact | feedback
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// MemoryRepo wraps all reads/writes for the memories table.
type MemoryRepo struct {
	db *sql.DB
}

// NewMemoryRepo creates a MemoryRepo.
func NewMemoryRepo(db *sql.DB) *MemoryRepo { return &MemoryRepo{db: db} }

// Save writes a long-term memory entry.
func (r *MemoryRepo) Save(ctx context.Context, m Memory) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO memories(id,agent_id,scope,session_id,kind,content,created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		m.ID, nullStr(m.AgentID), m.Scope, nullStr(m.SessionID), m.Kind, m.Content,
		m.CreatedAt.UnixMilli())
	return err
}

// Search retrieves memories by agent and scope.
func (r *MemoryRepo) Search(ctx context.Context, agentID, scope string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id,COALESCE(agent_id,''),scope,COALESCE(session_id,''),COALESCE(kind,''),content,created_at
		 FROM memories WHERE (agent_id=? OR scope='global') AND scope=?
		 ORDER BY created_at DESC LIMIT ?`,
		agentID, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mems []Memory
	for rows.Next() {
		var m Memory
		var createdMs int64
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Scope, &m.SessionID,
			&m.Kind, &m.Content, &createdMs); err != nil {
			return nil, err
		}
		m.CreatedAt = time.UnixMilli(createdMs)
		mems = append(mems, m)
	}
	return mems, rows.Err()
}
