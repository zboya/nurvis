package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/zboya/nurvis/internal/scheduler"
	"github.com/zboya/nurvis/internal/store/repo"
	"github.com/zboya/nurvis/internal/tools"
)

// cronToolAdapter adapts *scheduler.Scheduler to the tools.CronManager interface.
//
// Same lazy-binding rationale as channelToolAdapter: the tool registry (step 6) is
// built before the scheduler (step 11), so a shell is registered first and the real
// scheduler is injected via setScheduler() once ready. Calling the tool before
// injection returns a friendly "unavailable" error to the model.
type cronToolAdapter struct {
	mu    sync.RWMutex
	sched *scheduler.Scheduler
}

func (a *cronToolAdapter) setScheduler(s *scheduler.Scheduler) {
	a.mu.Lock()
	a.sched = s
	a.mu.Unlock()
}

func (a *cronToolAdapter) get() *scheduler.Scheduler {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sched
}

// Compile-time check that the adapter satisfies tools.CronManager.
var _ tools.CronManager = (*cronToolAdapter)(nil)

func (a *cronToolAdapter) AddJob(ctx context.Context, j repo.Job) (*repo.Job, error) {
	s := a.get()
	if s == nil {
		return nil, fmt.Errorf("scheduler not ready")
	}
	return s.AddJob(ctx, j)
}

func (a *cronToolAdapter) DeleteJob(ctx context.Context, id string) error {
	s := a.get()
	if s == nil {
		return fmt.Errorf("scheduler not ready")
	}
	return s.DeleteJob(ctx, id)
}

func (a *cronToolAdapter) ToggleJob(ctx context.Context, id string, enabled bool) error {
	s := a.get()
	if s == nil {
		return fmt.Errorf("scheduler not ready")
	}
	return s.ToggleJob(ctx, id, enabled)
}

func (a *cronToolAdapter) ListJobs(ctx context.Context) ([]repo.Job, error) {
	s := a.get()
	if s == nil {
		return nil, fmt.Errorf("scheduler not ready")
	}
	return s.ListJobs(ctx)
}

func (a *cronToolAdapter) ListRuns(ctx context.Context, jobID string, limit int) ([]repo.RunRecord, error) {
	s := a.get()
	if s == nil {
		return nil, fmt.Errorf("scheduler not ready")
	}
	return s.ListRuns(ctx, jobID, limit)
}

func (a *cronToolAdapter) RunNow(ctx context.Context, id string) error {
	s := a.get()
	if s == nil {
		return fmt.Errorf("scheduler not ready")
	}
	return s.RunNow(ctx, id)
}
