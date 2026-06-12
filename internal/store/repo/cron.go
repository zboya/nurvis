package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Job represents a row in the cron_jobs table.
type Job struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Spec      string `json:"spec"`
	AgentID   string `json:"agent_id"`
	ProjectID string `json:"project_id,omitempty"`
	Prompt    string `json:"prompt"`
	Enabled   bool   `json:"enabled"`

	// Target* triple: used to make cron-triggered Loops behave like "passive replies",
	// auto-locating the reply target. Combined with Scope.ChannelID / Scope.ReplyTo,
	// this allows the Agent's channel.send tool to hit the target directly.
	// When all three fields are empty, the loop is purely triggered (no channel binding),
	// behaving the same as unconfigured.
	TargetChannelID string `json:"target_channel_id,omitempty"`
	TargetPeerID    string `json:"target_peer_id,omitempty"`
	TargetPeerType  string `json:"target_peer_type,omitempty"` // user | group
}

// RunRecord represents a row in the cron_runs table.
type RunRecord struct {
	ID         string     `json:"id"`
	JobID      string     `json:"job_id"`
	SessionID  string     `json:"session_id,omitempty"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// CronRepo wraps all reads/writes for the cron_jobs / cron_runs tables.
type CronRepo struct {
	db *sql.DB
}

// NewCronRepo creates a CronRepo.
func NewCronRepo(db *sql.DB) *CronRepo { return &CronRepo{db: db} }

const jobColumns = `id,name,spec,agent_id,COALESCE(project_id,''),prompt,enabled,` +
	`COALESCE(target_channel_id,''),COALESCE(target_peer_id,''),COALESCE(target_peer_type,'')`

// CreateJob inserts a new job (enabled by default).
func (r *CronRepo) CreateJob(ctx context.Context, j Job) (*Job, error) {
	j.ID = uuid.New().String()
	j.Enabled = true
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO cron_jobs(
			id,name,spec,agent_id,project_id,prompt,enabled,created_at,
			target_channel_id,target_peer_id,target_peer_type
		 ) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Name, j.Spec, j.AgentID, nullStr(j.ProjectID), j.Prompt,
		1, time.Now().UnixMilli(),
		nullStr(j.TargetChannelID), nullStr(j.TargetPeerID), nullStr(j.TargetPeerType))
	if err != nil {
		return nil, fmt.Errorf("repo.cron: insert job: %w", err)
	}
	return &j, nil
}

// DeleteJob deletes a job and its run records.
func (r *CronRepo) DeleteJob(ctx context.Context, id string) error {
	_, _ = r.db.ExecContext(ctx, `DELETE FROM cron_runs WHERE job_id=?`, id)
	_, err := r.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id=?`, id)
	return err
}

// SetEnabled enables/disables a job.
func (r *CronRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE cron_jobs SET enabled=? WHERE id=?`, boolInt(enabled), id)
	return err
}

// ListJobs returns all jobs.
func (r *CronRepo) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobColumns+` FROM cron_jobs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows.Scan)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *j)
	}
	return jobs, rows.Err()
}

// GetJob retrieves a job by ID.
func (r *CronRepo) GetJob(ctx context.Context, id string) (*Job, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM cron_jobs WHERE id=?`, id)
	j, err := scanJob(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("repo.cron: get job %s: %w", id, err)
	}
	return j, nil
}

// StartRun records the start of a job run.
func (r *CronRepo) StartRun(ctx context.Context, runID, jobID string, startedAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO cron_runs(id,job_id,status,started_at) VALUES(?,?,?,?)`,
		runID, jobID, "running", startedAt.UnixMilli())
	return err
}

// FinishRun updates the final status of a job run.
func (r *CronRepo) FinishRun(ctx context.Context, runID, status, errStr, sessionID string, finishedAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE cron_runs SET status=?,error=?,session_id=?,finished_at=? WHERE id=?`,
		status, errStr, sessionID, finishedAt.UnixMilli(), runID)
	return err
}

// ListRuns returns the run history for a job.
func (r *CronRepo) ListRuns(ctx context.Context, jobID string, limit int) ([]RunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id,job_id,session_id,status,error,started_at,finished_at
		 FROM cron_runs WHERE job_id=? ORDER BY started_at DESC LIMIT ?`,
		jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []RunRecord
	for rows.Next() {
		var rec RunRecord
		var startedMs int64
		var finishedMs sql.NullInt64
		var sessionID, errStr sql.NullString
		if err := rows.Scan(&rec.ID, &rec.JobID, &sessionID, &rec.Status,
			&errStr, &startedMs, &finishedMs); err != nil {
			return nil, err
		}
		rec.SessionID = sessionID.String
		rec.Error = errStr.String
		rec.StartedAt = time.UnixMilli(startedMs)
		if finishedMs.Valid {
			t := time.UnixMilli(finishedMs.Int64)
			rec.FinishedAt = &t
		}
		runs = append(runs, rec)
	}
	return runs, rows.Err()
}

func scanJob(scan func(...any) error) (*Job, error) {
	var j Job
	var enabled int
	if err := scan(&j.ID, &j.Name, &j.Spec, &j.AgentID,
		&j.ProjectID, &j.Prompt, &enabled,
		&j.TargetChannelID, &j.TargetPeerID, &j.TargetPeerType); err != nil {
		return nil, err
	}
	j.Enabled = enabled == 1
	return &j, nil
}
