package repo

import (
	"context"
	"database/sql"
)

// BuiltinToolRepo wraps reads/writes for the builtin_tools table.
type BuiltinToolRepo struct {
	db *sql.DB
}

// NewBuiltinToolRepo creates a BuiltinToolRepo.
func NewBuiltinToolRepo(db *sql.DB) *BuiltinToolRepo { return &BuiltinToolRepo{db: db} }

// SetEnabled enables/disables a builtin tool (upsert).
func (r *BuiltinToolRepo) SetEnabled(ctx context.Context, name string, enabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO builtin_tools(name,enabled) VALUES(?,?)
		 ON CONFLICT(name) DO UPDATE SET enabled=excluded.enabled`,
		name, boolInt(enabled))
	return err
}
