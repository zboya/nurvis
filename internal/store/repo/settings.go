package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// SettingsRepo provides read/write access to the settings table (global KV).
// The value_json column stores arbitrary JSON values; Get/Set generic helpers handle serialization.
type SettingsRepo struct {
	db *sql.DB
}

func NewSettingsRepo(db *sql.DB) *SettingsRepo {
	return &SettingsRepo{db: db}
}

// GetRaw returns the raw JSON bytes; returns (nil, nil) if the key does not exist.
func (r *SettingsRepo) GetRaw(ctx context.Context, key string) ([]byte, error) {
	var v string
	err := r.db.QueryRowContext(ctx,
		`SELECT value_json FROM settings WHERE key = ?`, key,
	).Scan(&v)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []byte(v), nil
}

// SetRaw writes raw JSON bytes (UPSERT).
func (r *SettingsRepo) SetRaw(ctx context.Context, key string, valueJSON []byte) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO settings(key, value_json, updated_at)
		 VALUES(?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json, updated_at=excluded.updated_at`,
		key, string(valueJSON), time.Now().UnixMilli(),
	)
	return err
}

// GetBool reads a boolean value; returns defaultVal when the key does not exist.
func (r *SettingsRepo) GetBool(ctx context.Context, key string, defaultVal bool) (bool, error) {
	raw, err := r.GetRaw(ctx, key)
	if err != nil {
		return defaultVal, err
	}
	if raw == nil {
		return defaultVal, nil
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return defaultVal, err
	}
	return v, nil
}

// SetBool writes a boolean value.
func (r *SettingsRepo) SetBool(ctx context.Context, key string, val bool) error {
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return r.SetRaw(ctx, key, b)
}
