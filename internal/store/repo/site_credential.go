package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// SiteCredential represents a site credential record.
type SiteCredential struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Provider   string `json:"provider"`    // cloudflare | netlify | vercel
	ConfigJSON string `json:"config_json"` // credential details JSON
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// SiteCredentialRepo provides CRUD operations for the site_credentials table.
type SiteCredentialRepo struct {
	db *sql.DB
}

func NewSiteCredentialRepo(db *sql.DB) *SiteCredentialRepo {
	return &SiteCredentialRepo{db: db}
}

// List returns all credentials (optionally filtered by provider).
func (r *SiteCredentialRepo) List(ctx context.Context, provider string) ([]SiteCredential, error) {
	query := `SELECT id, name, provider, config_json, enabled, created_at, updated_at FROM site_credentials`
	var args []any
	if provider != "" {
		query += ` WHERE provider = ?`
		args = append(args, provider)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []SiteCredential
	for rows.Next() {
		var c SiteCredential
		var enabled int
		if err := rows.Scan(&c.ID, &c.Name, &c.Provider, &c.ConfigJSON, &enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Enabled = enabled == 1
		list = append(list, c)
	}
	return list, rows.Err()
}

// GetByID retrieves a single credential.
func (r *SiteCredentialRepo) GetByID(ctx context.Context, id string) (*SiteCredential, error) {
	var c SiteCredential
	var enabled int
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, provider, config_json, enabled, created_at, updated_at FROM site_credentials WHERE id = ?`, id,
	).Scan(&c.ID, &c.Name, &c.Provider, &c.ConfigJSON, &enabled, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Enabled = enabled == 1
	return &c, nil
}

// LookupByProvider finds the first enabled credential by provider (optionally matching name exactly).
func (r *SiteCredentialRepo) LookupByProvider(ctx context.Context, provider, name string) (*SiteCredential, error) {
	query := `SELECT id, name, provider, config_json, enabled, created_at, updated_at 
	          FROM site_credentials WHERE provider = ? AND enabled = 1`
	args := []any{provider}
	if name != "" {
		query += ` AND name = ?`
		args = append(args, name)
	}
	query += ` ORDER BY updated_at DESC LIMIT 1`

	var c SiteCredential
	var enabled int
	err := r.db.QueryRowContext(ctx, query, args...).Scan(
		&c.ID, &c.Name, &c.Provider, &c.ConfigJSON, &enabled, &c.CreatedAt, &c.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Enabled = enabled == 1
	return &c, nil
}

// Create creates a new credential.
func (r *SiteCredentialRepo) Create(ctx context.Context, name, provider, configJSON string) (*SiteCredential, error) {
	now := time.Now().UnixMilli()
	id := uuid.New().String()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO site_credentials(id, name, provider, config_json, enabled, created_at, updated_at)
		 VALUES(?, ?, ?, ?, 1, ?, ?)`,
		id, name, provider, configJSON, now, now,
	)
	if err != nil {
		return nil, err
	}
	return &SiteCredential{
		ID: id, Name: name, Provider: provider,
		ConfigJSON: configJSON, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// Update updates a credential.
func (r *SiteCredentialRepo) Update(ctx context.Context, id, name, configJSON string, enabled *bool) error {
	now := time.Now().UnixMilli()
	if enabled != nil {
		_, err := r.db.ExecContext(ctx,
			`UPDATE site_credentials SET name=?, config_json=?, enabled=?, updated_at=? WHERE id=?`,
			name, configJSON, boolInt(*enabled), now, id,
		)
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE site_credentials SET name=?, config_json=?, updated_at=? WHERE id=?`,
		name, configJSON, now, id,
	)
	return err
}

// Delete removes a credential.
func (r *SiteCredentialRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM site_credentials WHERE id = ?`, id)
	return err
}
