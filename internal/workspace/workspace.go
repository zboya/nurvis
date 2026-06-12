// Package workspace manages project (workspace) local directories.
package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/zboya/nurvis/internal/store/repo"
)

// Project model is defined in the repo package; type alias keeps external compatibility.
type Project = repo.Project

// Manager manages workspaces.
type Manager interface {
	Create(ctx context.Context, name, dir, description string) (*Project, error)
	List(ctx context.Context) ([]Project, error)
	Get(ctx context.Context, id string) (*Project, error)
	Update(ctx context.Context, id, name, dir, description string) (*Project, error)
	Delete(ctx context.Context, id string) error
	// Resolve returns the project and validates that its directory exists and is writable.
	Resolve(ctx context.Context, id string) (*Project, error)
}

type manager struct {
	repo *repo.ProjectRepo
}

// New creates a WorkspaceManager.
func New(db *sql.DB) Manager {
	return &manager{repo: repo.NewProjectRepo(db)}
}

func (m *manager) Create(ctx context.Context, name, dir, description string) (*Project, error) {
	if err := validateDir(dir); err != nil {
		return nil, err
	}
	return m.repo.Create(ctx, repo.Project{
		Name:        name,
		Dir:         dir,
		Description: description,
	})
}

func (m *manager) List(ctx context.Context) ([]Project, error) {
	return m.repo.List(ctx)
}

func (m *manager) Get(ctx context.Context, id string) (*Project, error) {
	return m.repo.Get(ctx, id)
}

func (m *manager) Update(ctx context.Context, id, name, dir, description string) (*Project, error) {
	if dir != "" {
		if err := validateDir(dir); err != nil {
			return nil, err
		}
	}
	return m.repo.Update(ctx, id, name, dir, description)
}

func (m *manager) Delete(ctx context.Context, id string) error {
	return m.repo.Delete(ctx, id)
}

func (m *manager) Resolve(ctx context.Context, id string) (*Project, error) {
	p, err := m.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := validateDir(p.Dir); err != nil {
		return nil, fmt.Errorf("workspace: resolve %q: %w", p.Dir, err)
	}
	return p, nil
}

func validateDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("workspace: directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace: %q is not a directory", dir)
	}
	return nil
}
