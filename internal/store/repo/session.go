package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Session represents a row in the sessions table.
type Session struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	ProjectID string    `json:"project_id,omitempty"`
	Label     string    `json:"label,omitempty"`
	Channel   string    `json:"channel,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionRepo wraps all reads/writes for the sessions table.
type SessionRepo struct {
	db *sql.DB
}

// NewSessionRepo creates a SessionRepo.
func NewSessionRepo(db *sql.DB) *SessionRepo { return &SessionRepo{db: db} }

// Create creates a new session and returns a copy with ID/timestamps filled.
func (r *SessionRepo) Create(ctx context.Context, s Session) (*Session, error) {
	s.ID = uuid.New().String()
	now := time.Now()
	s.CreatedAt = now
	s.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions(id,agent_id,project_id,channel,created_at,updated_at)
		 VALUES(?,?,?,?,?,?)`,
		s.ID, s.AgentID, nullStr(s.ProjectID), s.Channel, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Exists checks whether a session exists.
func (r *SessionRepo) Exists(ctx context.Context, id string) (bool, error) {
	var sid string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id=?`, id).Scan(&sid)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Get retrieves a session by ID; returns (nil, nil) when not found, making it convenient for "read but allow missing" usage.
func (r *SessionRepo) Get(ctx context.Context, id string) (*Session, error) {
	const cols = `id,agent_id,COALESCE(project_id,''),COALESCE(label,''),
		COALESCE(channel,''),COALESCE(summary,''),created_at,updated_at`
	row := r.db.QueryRowContext(ctx, `SELECT `+cols+` FROM sessions WHERE id=?`, id)
	var s Session
	var createdMs, updatedMs int64
	err := row.Scan(&s.ID, &s.AgentID, &s.ProjectID, &s.Label,
		&s.Channel, &s.Summary, &createdMs, &updatedMs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.CreatedAt = time.UnixMilli(createdMs)
	s.UpdatedAt = time.UnixMilli(updatedMs)
	return &s, nil
}

// EnsureCreated creates the session if it does not exist (used in Agent Loop context stage).
func (r *SessionRepo) EnsureCreated(ctx context.Context, s Session) error {
	exists, err := r.Exists(ctx, s.ID)
	if err != nil || exists {
		return err
	}
	now := time.Now().UnixMilli()
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO sessions(id,agent_id,project_id,channel,created_at,updated_at)
		 VALUES(?,?,?,?,?,?)`,
		s.ID, s.AgentID, nullStr(s.ProjectID), s.Channel, now, now)
	return err
}

// List retrieves sessions; empty agentID lists all.
func (r *SessionRepo) List(ctx context.Context, agentID string, limit int) ([]Session, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const cols = `id,agent_id,COALESCE(project_id,''),COALESCE(label,''),
		COALESCE(channel,''),COALESCE(summary,''),created_at,updated_at`
	var (
		rows *sql.Rows
		err  error
	)
	if agentID != "" {
		rows, err = r.db.QueryContext(ctx,
			`SELECT `+cols+` FROM sessions WHERE agent_id=? ORDER BY updated_at DESC LIMIT ?`,
			agentID, limit)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT `+cols+` FROM sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var s Session
		var createdMs, updatedMs int64
		if err := rows.Scan(&s.ID, &s.AgentID, &s.ProjectID, &s.Label,
			&s.Channel, &s.Summary, &createdMs, &updatedMs); err != nil {
			return nil, err
		}
		s.CreatedAt = time.UnixMilli(createdMs)
		s.UpdatedAt = time.UnixMilli(updatedMs)
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// Delete removes a session and its associated messages.
func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	_, _ = r.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id=?`, id)
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

// SetLabel updates the session label.
func (r *SessionRepo) SetLabel(ctx context.Context, id, label string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET label=?,updated_at=? WHERE id=?`,
		label, time.Now().UnixMilli(), id)
	return err
}

// Touch updates the session's updated_at.
func (r *SessionRepo) Touch(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET updated_at=? WHERE id=?`, at.UnixMilli(), id)
	return err
}

// SetSummary updates the session rolling summary.
func (r *SessionRepo) SetSummary(ctx context.Context, id, summary string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET summary=? WHERE id=?`, summary, id)
	return err
}
