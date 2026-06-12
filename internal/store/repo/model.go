package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// ModelRepo persists the lifecycle of a model download (gguf pull) and
// doubles as the registry of installed models.
//
// The table is used to:
//   - Resume the UI after a backend restart (List / ListActive).
//   - Let the user manually retry an interrupted download (no automatic resume).
//   - Throttle progress updates (UpdateProgress).
//   - Serve `models.list` directly from DB (rows with status='success').
type ModelRepo struct {
	db *sql.DB
}

func NewModelRepo(db *sql.DB) *ModelRepo {
	return &ModelRepo{db: db}
}

// Model mirrors a row in models.
type Model struct {
	Model        string  `json:"model"`
	Repo         string  `json:"repo"`
	File         string  `json:"file"`
	Status       string  `json:"status"`
	TotalBytes   int64   `json:"total"`
	CurrentBytes int64   `json:"current"`
	Percent      float64 `json:"percent"`
	Error        string  `json:"error,omitempty"`
	StartedAt    int64   `json:"started_at"`
	UpdatedAt    int64   `json:"updated_at"`
	FinishedAt   int64   `json:"finished_at,omitempty"`

	// HuggingFace metadata.
	PipelineTag string   `json:"pipeline_tag,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Modalities  []string `json:"modalities,omitempty"` // text/image/audio/video

	// Local file metadata (populated when status='success').
	LocalPath string `json:"local_path,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"` // actual on-disk size
}

// UpsertStart records (or restarts) a pull job for the given model.
// Existing terminal rows for the same model are reset back to "queued".
// HF metadata fields are preserved on retry; they will be overwritten by
// UpsertMetadata when fresh data is fetched.
func (r *ModelRepo) UpsertStart(ctx context.Context, model, repo, file string) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO models (model, repo, file, status, total_bytes, current_bytes, percent, error, started_at, updated_at, finished_at)
		VALUES (?, ?, ?, 'queued', 0, 0, 0, NULL, ?, ?, NULL)
		ON CONFLICT(model) DO UPDATE SET
			repo=excluded.repo,
			file=excluded.file,
			status='queued',
			total_bytes=0,
			current_bytes=0,
			percent=0,
			error=NULL,
			started_at=excluded.started_at,
			updated_at=excluded.updated_at,
			finished_at=NULL
	`, model, repo, file, now, now)
	return err
}

// UpsertMetadata stores HuggingFace-derived metadata for the model. Called
// during the resolving phase of a pull, after fetching the model detail API.
func (r *ModelRepo) UpsertMetadata(ctx context.Context, model, pipelineTag string, tags, modalities []string) error {
	tagsJSON, _ := json.Marshal(tags)
	modJSON, _ := json.Marshal(modalities)
	_, err := r.db.ExecContext(ctx, `
		UPDATE models SET pipeline_tag=?, tags_json=?, modalities_json=?, updated_at=?
		WHERE model=?
	`, pipelineTag, string(tagsJSON), string(modJSON), time.Now().UnixMilli(), model)
	return err
}

// UpdateProgress writes the latest progress snapshot. Caller is expected to
// throttle calls (e.g. ~1/second) — the gateway already throttles its bus
// publishes, so the simplest pattern is to call this from the same throttled
// branch.
func (r *ModelRepo) UpdateProgress(ctx context.Context, model, status string, current, total int64, percent float64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE models SET status=?, current_bytes=?, total_bytes=?, percent=?, updated_at=?
		WHERE model=?
	`, status, current, total, percent, time.Now().UnixMilli(), model)
	return err
}

// SuccessMeta is the bundle of post-download metadata persisted on success.
type SuccessMeta struct {
	LocalPath string
	SizeBytes int64 // actual on-disk size
}

// MarkSuccess marks the pull as completed and stores local file path + size.
// The row then doubles as an installed-model registry entry served by
// ListSuccess.
func (r *ModelRepo) MarkSuccess(ctx context.Context, model string, m SuccessMeta) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx, `
		UPDATE models SET
			status='success',
			current_bytes=?, total_bytes=?, percent=100, error=NULL,
			updated_at=?, finished_at=?,
			local_path=?, size_bytes=?
		WHERE model=?
	`, m.SizeBytes, m.SizeBytes, now, now,
		m.LocalPath, m.SizeBytes, model)
	return err
}

// MarkError marks the pull as failed with the given error message.
func (r *ModelRepo) MarkError(ctx context.Context, model, errMsg string) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx, `
		UPDATE models SET status='error', error=?, updated_at=?, finished_at=?
		WHERE model=?
	`, errMsg, now, now, model)
	return err
}

// MarkInterruptedAll downgrades any non-terminal rows to "interrupted".
// Called once at startup so leftover rows from a crashed previous run do not
// keep showing as "downloading" forever.
func (r *ModelRepo) MarkInterruptedAll(ctx context.Context) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx, `
		UPDATE models SET status='interrupted', updated_at=?, finished_at=?,
		                       error=COALESCE(error, '进程已重启，下载已中断')
		WHERE status NOT IN ('success','error','interrupted')
	`, now, now)
	return err
}

// Delete removes a pull row (e.g. when the user dismisses the card).
func (r *ModelRepo) Delete(ctx context.Context, model string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM models WHERE model=?`, model)
	return err
}

// Get fetches a single row by model id. Returns sql.ErrNoRows if absent.
func (r *ModelRepo) Get(ctx context.Context, model string) (*Model, error) {
	row := r.db.QueryRowContext(ctx, selectAllCols+` WHERE model=?`, model)
	p, err := scanModel(row)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// List returns all pull rows ordered by start time ascending.
func (r *ModelRepo) List(ctx context.Context) ([]Model, error) {
	return r.queryRows(ctx, selectAllCols+` ORDER BY started_at ASC`)
}

// ListSuccess returns all rows whose status is 'success' — i.e. the set of
// installed models. Ordered by model name for stable listing.
func (r *ModelRepo) ListSuccess(ctx context.Context) ([]Model, error) {
	return r.queryRows(ctx, selectAllCols+` WHERE status='success' ORDER BY model ASC`)
}

const selectAllCols = `
	SELECT model, repo, file, status, total_bytes, current_bytes, percent,
	       COALESCE(error,''), started_at, updated_at, COALESCE(finished_at, 0),
	       COALESCE(pipeline_tag,''), COALESCE(tags_json,''), COALESCE(modalities_json,''),
	       COALESCE(local_path,''), COALESCE(size_bytes, 0)
	FROM models`

func (r *ModelRepo) queryRows(ctx context.Context, query string, args ...any) ([]Model, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		p, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// scanner is implemented by both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanModel(s scanner) (Model, error) {
	var (
		p                 Model
		tagsJSON, modJSON string
		sizeBytes         int64
	)
	if err := s.Scan(&p.Model, &p.Repo, &p.File, &p.Status,
		&p.TotalBytes, &p.CurrentBytes, &p.Percent,
		&p.Error, &p.StartedAt, &p.UpdatedAt, &p.FinishedAt,
		&p.PipelineTag, &tagsJSON, &modJSON,
		&p.LocalPath, &sizeBytes,
	); err != nil {
		return p, err
	}
	p.SizeBytes = sizeBytes
	if tagsJSON != "" {
		_ = json.Unmarshal([]byte(tagsJSON), &p.Tags)
	}
	if modJSON != "" {
		_ = json.Unmarshal([]byte(modJSON), &p.Modalities)
	}
	return p, nil
}
