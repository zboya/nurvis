package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Message represents a row in the messages table (simplified view).
type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	ToolCalls any       `json:"tool_calls,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	MediaJSON string    `json:"media_json,omitempty"` // attachment file paths JSON, maps to media_json column
	Tokens    int       `json:"tokens,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// MessageRepo wraps all reads/writes for the messages table.
type MessageRepo struct {
	db *sql.DB
}

// NewMessageRepo creates a MessageRepo.
func NewMessageRepo(db *sql.DB) *MessageRepo { return &MessageRepo{db: db} }

// Save persists a message (INSERT OR IGNORE to prevent duplicates).
func (r *MessageRepo) Save(ctx context.Context, m Message) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	tcJSON := ""
	if m.ToolCalls != nil {
		b, _ := json.Marshal(m.ToolCalls)
		tcJSON = string(b)
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO messages(id,session_id,role,content,tool_calls_json,tool_name,media_json,tokens,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		m.ID, m.SessionID, m.Role, m.Content, tcJSON, m.ToolName, m.MediaJSON, m.Tokens,
		m.CreatedAt.UnixMilli())
	return err
}

// List retrieves session history (most recent limit messages, returned in ascending order).
func (r *MessageRepo) List(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id,session_id,role,content,COALESCE(tool_calls_json,''),COALESCE(tool_name,''),
		  COALESCE(media_json,''),COALESCE(tokens,0),created_at
		 FROM messages WHERE session_id=?
		 ORDER BY created_at DESC LIMIT ?`,
		sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	reverse(msgs)
	return msgs, nil
}

// ListBefore retrieves session history with cursor-based pagination (before=0 starts from latest), returned in ascending order.
func (r *MessageRepo) ListBefore(ctx context.Context, sessionID string, before int64, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var (
		rows *sql.Rows
		err  error
	)
	if before > 0 {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id,session_id,role,content,COALESCE(tool_calls_json,''),COALESCE(tool_name,''),
			  COALESCE(media_json,''),COALESCE(tokens,0),created_at
			 FROM messages WHERE session_id=? AND created_at<? ORDER BY created_at DESC LIMIT ?`,
			sessionID, before, limit)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id,session_id,role,content,COALESCE(tool_calls_json,''),COALESCE(tool_name,''),
			  COALESCE(media_json,''),COALESCE(tokens,0),created_at
			 FROM messages WHERE session_id=? ORDER BY created_at DESC LIMIT ?`,
			sessionID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	reverse(msgs)
	return msgs, nil
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var msgs []Message
	for rows.Next() {
		var m Message
		var createdMs int64
		var tcJSON string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content,
			&tcJSON, &m.ToolName, &m.MediaJSON, &m.Tokens, &createdMs); err != nil {
			return nil, err
		}
		m.CreatedAt = time.UnixMilli(createdMs)
		if tcJSON != "" {
			var tc any
			if json.Unmarshal([]byte(tcJSON), &tc) == nil {
				m.ToolCalls = tc
			}
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func reverse(msgs []Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}
