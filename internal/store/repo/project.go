package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Project represents a row in the projects table.
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Dir         string    `json:"dir"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProjectRepo wraps all reads/writes for the projects table.
type ProjectRepo struct {
	db *sql.DB
}

// NewProjectRepo creates a ProjectRepo.
func NewProjectRepo(db *sql.DB) *ProjectRepo { return &ProjectRepo{db: db} }

const projectColumns = `id,name,dir,description,created_at,updated_at`

// Create inserts a new Project.
func (r *ProjectRepo) Create(ctx context.Context, p Project) (*Project, error) {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	p.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO projects(`+projectColumns+`) VALUES(?,?,?,?,?,?)`,
		p.ID, p.Name, p.Dir, p.Description,
		p.CreatedAt.UnixMilli(), p.UpdatedAt.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("repo.project: create: %w", err)
	}
	return &p, nil
}

// List returns all Projects sorted by creation time descending.
func (r *ProjectRepo) List(ctx context.Context) ([]Project, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+projectColumns+` FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		p, err := scanProjectRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		projects = append(projects, *p)
	}
	return projects, rows.Err()
}

// Get retrieves a Project by ID.
func (r *ProjectRepo) Get(ctx context.Context, id string) (*Project, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id=?`, id)
	return scanProjectRow(row.Scan)
}

// Update updates a Project; empty name/dir keeps the original value.
func (r *ProjectRepo) Update(ctx context.Context, id, name, dir, description string) (*Project, error) {
	_, err := r.db.ExecContext(ctx,
		`UPDATE projects SET name=COALESCE(NULLIF(?,CAST('' AS TEXT)),name),
		  dir=COALESCE(NULLIF(?,CAST('' AS TEXT)),dir),
		  description=?,updated_at=? WHERE id=?`,
		name, dir, description, time.Now().UnixMilli(), id)
	if err != nil {
		return nil, fmt.Errorf("repo.project: update: %w", err)
	}
	return r.Get(ctx, id)
}

// Delete removes a Project and nullifies references to it.
func (r *ProjectRepo) Delete(ctx context.Context, id string) error {
	_, _ = r.db.ExecContext(ctx, `UPDATE agents SET default_project=NULL WHERE default_project=?`, id)
	_, _ = r.db.ExecContext(ctx, `UPDATE sessions SET project_id=NULL WHERE project_id=?`, id)
	_, _ = r.db.ExecContext(ctx, `UPDATE cron_jobs SET project_id=NULL WHERE project_id=?`, id)
	_, err := r.db.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)
	return err
}

func scanProjectRow(scan func(...any) error) (*Project, error) {
	var p Project
	var createdMs, updatedMs int64
	if err := scan(&p.ID, &p.Name, &p.Dir, &p.Description, &createdMs, &updatedMs); err != nil {
		return nil, fmt.Errorf("repo.project: scan: %w", err)
	}
	p.CreatedAt = time.UnixMilli(createdMs)
	p.UpdatedAt = time.UnixMilli(updatedMs)
	return &p, nil
}
