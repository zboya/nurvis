// Package scheduler implements persistent cron jobs based on robfig/cron/v3.
// On startup it loads all enabled jobs from SQLite and triggers Agent Loop via agent.Manager.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/store/repo"
)

// Job and RunRecord models are defined in the repo package; type aliases are used here for compatibility.
type (
	Job       = repo.Job
	RunRecord = repo.RunRecord
)

// Dispatcher triggers an Agent Loop, implemented by agent.Manager.
// Unpacked fields are used to avoid a circular dependency between scheduler and agent.
//
// channelID / peerID / peerType are optional "target peer" identifiers — they allow a
// cron-triggered Loop to bind the channel.send tool's default target to a specific channel
// and peer, so the model only needs to decide "what to send". When all three are empty,
// no channel is bound (behavior is identical to before this change).
type Dispatcher interface {
	DispatchCronJob(
		ctx context.Context,
		agentID, projectID, prompt string,
		channelID, peerID, peerType string,
	) (string, error)
}

// Scheduler is a persistent cron job manager.
type Scheduler struct {
	repo       *repo.CronRepo
	bus        bus.Bus
	dispatcher Dispatcher
	cron       *cron.Cron
	mu         sync.Mutex
	entryMap   map[string]cron.EntryID // jobID → cron.EntryID

	// rootCtx is captured from Start(ctx) and used as parent for fire().
	// When the embedding application cancels this ctx, in-flight cron-triggered
	// dispatches receive cancellation as well.
	rootCtx context.Context
}

// New creates a new Scheduler.
func New(db *sql.DB, b bus.Bus, d Dispatcher) *Scheduler {
	c := cron.New(cron.WithSeconds())
	return &Scheduler{
		repo:       repo.NewCronRepo(db),
		bus:        b,
		dispatcher: d,
		cron:       c,
		entryMap:   make(map[string]cron.EntryID),
		rootCtx:    context.Background(),
	}
}

// Start loads jobs from DB and starts the cron engine.
func (s *Scheduler) Start(ctx context.Context) error {
	if ctx != nil {
		s.rootCtx = ctx
	}
	jobs, err := s.listJobs(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: load jobs: %w", err)
	}
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		if err := s.addEntry(j); err != nil {
			slog.Warn("scheduler: add entry failed", "job", j.Name, "err", err)
		}
	}
	s.cron.Start()
	slog.Info("scheduler: started", "jobs", len(s.entryMap))
	return nil
}

// Stop stops the cron engine (waits for running jobs to complete).
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

// AddJob saves a new job to DB and registers it with the cron engine immediately.
func (s *Scheduler) AddJob(ctx context.Context, j Job) (*Job, error) {
	created, err := s.repo.CreateJob(ctx, j)
	if err != nil {
		return nil, fmt.Errorf("scheduler: insert job: %w", err)
	}
	if err := s.addEntry(*created); err != nil {
		// Roll back DB write so an invalid spec does not leave a dangling row
		// that will fail again on next startup.
		_ = s.repo.DeleteJob(ctx, created.ID)
		return nil, err
	}
	return created, nil
}

// DeleteJob removes a job from DB and the cron engine.
func (s *Scheduler) DeleteJob(ctx context.Context, id string) error {
	s.mu.Lock()
	if entryID, ok := s.entryMap[id]; ok {
		s.cron.Remove(entryID)
		delete(s.entryMap, id)
	}
	s.mu.Unlock()

	return s.repo.DeleteJob(ctx, id)
}

// ToggleJob enables or disables a job.
func (s *Scheduler) ToggleJob(ctx context.Context, id string, enabled bool) error {
	j, err := s.getJob(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}
	s.mu.Lock()
	_, hasEntry := s.entryMap[id]
	if !enabled {
		if hasEntry {
			entryID := s.entryMap[id]
			s.cron.Remove(entryID)
			delete(s.entryMap, id)
		}
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	// addEntry takes s.mu itself, so call it without holding the lock to avoid
	// a self-deadlock.
	if !hasEntry {
		if err := s.addEntry(*j); err != nil {
			// Roll back the enabled flag so DB and cron engine stay in sync.
			_ = s.repo.SetEnabled(ctx, id, false)
			return err
		}
	}
	return nil
}

// ListJobs returns all jobs.
func (s *Scheduler) ListJobs(ctx context.Context) ([]Job, error) {
	return s.listJobs(ctx)
}

// ListRuns returns the run history of a job.
func (s *Scheduler) ListRuns(ctx context.Context, jobID string, limit int) ([]RunRecord, error) {
	return s.repo.ListRuns(ctx, jobID, limit)
}

// RunNow executes a job immediately (bypassing cron scheduling).
func (s *Scheduler) RunNow(ctx context.Context, id string) error {
	j, err := s.getJob(ctx, id)
	if err != nil {
		return err
	}
	go s.fire(j)
	return nil
}

// ── internal ──────────────────────────────────────────────────────────────────

func (s *Scheduler) addEntry(j Job) error {
	entryID, err := s.cron.AddFunc(j.Spec, func() {
		s.fire(&j)
	})
	if err != nil {
		return fmt.Errorf("scheduler: invalid cron spec %q: %w", j.Spec, err)
	}
	s.mu.Lock()
	s.entryMap[j.ID] = entryID
	s.mu.Unlock()
	return nil
}

func (s *Scheduler) fire(j *Job) {
	ctx := s.rootCtx
	if ctx == nil {
		ctx = context.Background()
	}
	runID := uuid.New().String()
	startedAt := time.Now()

	s.bus.Publish(bus.TopicCronFired, map[string]any{
		"job_id": j.ID,
		"name":   j.Name,
		"run_id": runID,
	})

	// Record run start
	_ = s.repo.StartRun(ctx, runID, j.ID, startedAt)

	sessionID, err := s.dispatcher.DispatchCronJob(
		ctx,
		j.AgentID, j.ProjectID, j.Prompt,
		j.TargetChannelID, j.TargetPeerID, j.TargetPeerType,
	)
	status := "ok"
	errStr := ""
	if err != nil {
		status = "failed"
		errStr = err.Error()
		slog.Warn("scheduler: job failed", "job", j.Name, "err", err)
	}

	// Update run record
	_ = s.repo.FinishRun(ctx, runID, status, errStr, sessionID, time.Now())
}

func (s *Scheduler) listJobs(ctx context.Context) ([]Job, error) {
	return s.repo.ListJobs(ctx)
}

func (s *Scheduler) getJob(ctx context.Context, id string) (*Job, error) {
	return s.repo.GetJob(ctx, id)
}
