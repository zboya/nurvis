package repo

import (
	"context"
	"database/sql"
)

// SkillRecord represents a row in the skills table.
type SkillRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

// SkillRepo wraps reads/writes for the skills / skill_grants tables.
type SkillRepo struct {
	db *sql.DB
}

// NewSkillRepo creates a SkillRepo.
func NewSkillRepo(db *sql.DB) *SkillRepo { return &SkillRepo{db: db} }

// List returns all Skills.
func (r *SkillRepo) List(ctx context.Context) ([]SkillRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id,name,COALESCE(version,''),path,enabled FROM skills ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var skills []SkillRecord
	for rows.Next() {
		var s SkillRecord
		var enabled int
		if err := rows.Scan(&s.ID, &s.Name, &s.Version, &s.Path, &enabled); err != nil {
			return nil, err
		}
		s.Enabled = enabled == 1
		skills = append(skills, s)
	}
	return skills, rows.Err()
}

// SetEnabled enables/disables a Skill.
func (r *SkillRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE skills SET enabled=? WHERE id=?`, boolInt(enabled), id)
	return err
}

// Grant authorizes an agent to use a Skill.
func (r *SkillRepo) Grant(ctx context.Context, skillID, agentID string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO skill_grants(skill_id,agent_id) VALUES(?,?)`,
		skillID, agentID)
	return err
}

// Revoke removes an agent's authorization for a Skill.
func (r *SkillRepo) Revoke(ctx context.Context, skillID, agentID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM skill_grants WHERE skill_id=? AND agent_id=?`, skillID, agentID)
	return err
}

// ListGrantedSkillIDs returns all skill IDs authorized for an agent.
func (r *SkillRepo) ListGrantedSkillIDs(ctx context.Context, agentID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT skill_id FROM skill_grants WHERE agent_id=?`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
