package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Agent represents a row in the agents table.
type Agent struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Role           string         `json:"role,omitempty"`
	SystemPrompt   string         `json:"system_prompt,omitempty"`
	ProviderID     string         `json:"provider_id,omitempty"`
	Model          string         `json:"model"`
	DefaultProject string         `json:"default_project,omitempty"`
	Options        map[string]any `json:"options,omitempty"`
	MaxRounds      int            `json:"max_rounds"`
	Enabled        bool           `json:"enabled"`
	AllowedTools   []string       `json:"allowed_tools,omitempty"`
	// Tag classifies the agent's runtime modality.
	// One of: "to-text" (default), "to-image", "to-video".
	// Used by chat.send to choose between the standard llama loop and the
	// gosd image/video pipelines.
	Tag       string    `json:"tag,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// optionInt extracts an int from a free-form options map, accepting int / int64 /
// float64 / json.Number / numeric string. Falls back to defaultValue on miss.
func optionInt(m map[string]any, key string, defaultValue int) int {
	v, ok := m[key]
	if !ok || v == nil {
		return defaultValue
	}
	switch x := v.(type) {
	case int:
		if x > 0 {
			return x
		}
	case int32:
		if x > 0 {
			return int(x)
		}
	case int64:
		if x > 0 {
			return int(x)
		}
	case float64:
		if x > 0 {
			return int(x)
		}
	case float32:
		if x > 0 {
			return int(x)
		}
	}
	return defaultValue
}

func (a *Agent) GetContextWindow(defaultValue int) int {
	return optionInt(a.Options, "context_window", defaultValue)
}

func (a *Agent) GetMaxOutputTokens(defaultValue int) int {
	return optionInt(a.Options, "max_tokens", defaultValue)
}

func (a *Agent) GetReserveTokens(defaultValue int) int {
	return optionInt(a.Options, "reserve_tokens", defaultValue)
}

// AgentRepo wraps all reads/writes for the agents table.
type AgentRepo struct {
	db *sql.DB
}

// NewAgentRepo creates an AgentRepo.
func NewAgentRepo(db *sql.DB) *AgentRepo { return &AgentRepo{db: db} }

const agentColumns = `id,name,role,system_prompt,provider_id,model,default_project,
	options_json,max_rounds,enabled,tag,created_at,updated_at`

// Create inserts a new Agent and returns a copy with ID/timestamps filled.
func (r *AgentRepo) Create(ctx context.Context, a Agent) (*Agent, error) {
	a.ID = uuid.New().String()
	a.CreatedAt = time.Now()
	a.UpdatedAt = time.Now()
	if a.MaxRounds <= 0 {
		a.MaxRounds = 16
	}
	optJSON, _ := json.Marshal(a.Options)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("repo.agent: create tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if a.Tag == "" {
		a.Tag = "to-text"
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO agents(`+agentColumns+`)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.Role, a.SystemPrompt, nullStr(a.ProviderID),
		a.Model, nullStr(a.DefaultProject), string(optJSON),
		a.MaxRounds, boolInt(a.Enabled), a.Tag,
		a.CreatedAt.UnixMilli(), a.UpdatedAt.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("repo.agent: create: %w", err)
	}

	if err := syncAgentTools(ctx, tx, a.ID, a.AllowedTools); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("repo.agent: create commit: %w", err)
	}
	return &a, nil
}

// Get retrieves an Agent by ID.
func (r *AgentRepo) Get(ctx context.Context, id string) (*Agent, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+agentColumns+` FROM agents WHERE id=?`, id)
	a, err := scanAgentRow(row.Scan)
	if err != nil {
		return nil, err
	}
	a.AllowedTools, err = loadAgentTools(ctx, r.db, id)
	return a, err
}

// List returns all Agents sorted by creation time descending.
func (r *AgentRepo) List(ctx context.Context) ([]Agent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+agentColumns+` FROM agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []Agent
	for rows.Next() {
		a, err := scanAgentRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		agents = append(agents, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Batch-load tool whitelist for each agent
	for i := range agents {
		agents[i].AllowedTools, err = loadAgentTools(ctx, r.db, agents[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return agents, nil
}

// Update updates Agent fields and returns the updated record.
func (r *AgentRepo) Update(ctx context.Context, a Agent) (*Agent, error) {
	a.UpdatedAt = time.Now()
	optJSON, _ := json.Marshal(a.Options)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("repo.agent: update tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if a.Tag == "" {
		a.Tag = "to-text"
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE agents SET name=?,role=?,system_prompt=?,provider_id=?,model=?,
		  default_project=?,options_json=?,max_rounds=?,enabled=?,tag=?,updated_at=?
		 WHERE id=?`,
		a.Name, a.Role, a.SystemPrompt, nullStr(a.ProviderID), a.Model,
		nullStr(a.DefaultProject), string(optJSON), a.MaxRounds, boolInt(a.Enabled), a.Tag,
		a.UpdatedAt.UnixMilli(), a.ID)
	if err != nil {
		return nil, fmt.Errorf("repo.agent: update: %w", err)
	}

	if err := syncAgentTools(ctx, tx, a.ID, a.AllowedTools); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("repo.agent: update commit: %w", err)
	}
	return r.Get(ctx, a.ID)
}

// Delete removes an Agent and its related data (tool whitelist, grants, sessions, memories, cron jobs, etc.).
func (r *AgentRepo) Delete(ctx context.Context, id string) error {
	// Delete associated messages (via sessions)
	_, _ = r.db.ExecContext(ctx,
		`DELETE FROM messages WHERE session_id IN (SELECT id FROM sessions WHERE agent_id=?)`, id)
	// Delete associated cron_runs (via cron_jobs)
	_, _ = r.db.ExecContext(ctx,
		`DELETE FROM cron_runs WHERE job_id IN (SELECT id FROM cron_jobs WHERE agent_id=?)`, id)
	// Delete directly associated tables
	_, _ = r.db.ExecContext(ctx, `DELETE FROM agent_tools WHERE agent_id=?`, id)
	_, _ = r.db.ExecContext(ctx, `DELETE FROM mcp_grants WHERE agent_id=?`, id)
	_, _ = r.db.ExecContext(ctx, `DELETE FROM skill_grants WHERE agent_id=?`, id)
	_, _ = r.db.ExecContext(ctx, `DELETE FROM sessions WHERE agent_id=?`, id)
	_, _ = r.db.ExecContext(ctx, `DELETE FROM memories WHERE agent_id=?`, id)
	_, _ = r.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE agent_id=?`, id)
	// Nullify references (channels / channel_routes)
	_, _ = r.db.ExecContext(ctx, `UPDATE channels SET agent_id=NULL WHERE agent_id=?`, id)
	_, _ = r.db.ExecContext(ctx, `UPDATE channel_routes SET agent_id=NULL WHERE agent_id=?`, id)
	// Finally delete the agent itself
	_, err := r.db.ExecContext(ctx, `DELETE FROM agents WHERE id=?`, id)
	return err
}

// scanAgentRow parses one agent row using a unified scan function (row.Scan or rows.Scan).
func scanAgentRow(scan func(...any) error) (*Agent, error) {
	var a Agent
	var optJSON string
	var createdMs, updatedMs int64
	var enabled int
	var providerID, defaultProject sql.NullString
	var tag sql.NullString
	if err := scan(&a.ID, &a.Name, &a.Role, &a.SystemPrompt, &providerID,
		&a.Model, &defaultProject, &optJSON, &a.MaxRounds, &enabled, &tag,
		&createdMs, &updatedMs); err != nil {
		return nil, fmt.Errorf("repo.agent: scan: %w", err)
	}
	a.ProviderID = providerID.String
	a.DefaultProject = defaultProject.String
	a.Enabled = enabled == 1
	a.Tag = tag.String
	if a.Tag == "" {
		a.Tag = "to-text"
	}
	a.CreatedAt = time.UnixMilli(createdMs)
	a.UpdatedAt = time.UnixMilli(updatedMs)
	_ = json.Unmarshal([]byte(optJSON), &a.Options)
	return &a, nil
}

// syncAgentTools fully replaces the tool whitelist for the given agent within a transaction.
// When tools is nil or empty the whitelist is cleared (the loop layer handles unrestricted mode).
func syncAgentTools(ctx context.Context, tx *sql.Tx, agentID string, tools []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_tools WHERE agent_id=?`, agentID); err != nil {
		return fmt.Errorf("repo.agent: sync tools delete: %w", err)
	}
	for _, t := range tools {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO agent_tools(agent_id,tool_ref,enabled) VALUES(?,?,1)
			 ON CONFLICT(agent_id,tool_ref) DO UPDATE SET enabled=1`,
			agentID, t); err != nil {
			return fmt.Errorf("repo.agent: sync tools insert %q: %w", t, err)
		}
	}
	return nil
}

// loadAgentTools queries the enabled tool whitelist for the given agent.
func loadAgentTools(ctx context.Context, db *sql.DB, agentID string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT tool_ref FROM agent_tools WHERE agent_id=? AND enabled=1`, agentID)
	if err != nil {
		return nil, fmt.Errorf("repo.agent: load tools: %w", err)
	}
	defer rows.Close()
	var tools []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}