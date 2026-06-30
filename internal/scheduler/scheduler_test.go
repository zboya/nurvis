package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/zboya/nurvis/internal/bus"
	"github.com/zboya/nurvis/internal/store/repo"
)

// ── test helpers ──────────────────────────────────────────────────────────────

const cronSchema = `
CREATE TABLE IF NOT EXISTS cron_jobs (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    spec               TEXT NOT NULL,
    agent_id           TEXT NOT NULL,
    project_id         TEXT,
    prompt             TEXT NOT NULL,
    enabled            INTEGER DEFAULT 1,
    created_at         INTEGER NOT NULL,
    target_channel_id  TEXT,
    target_peer_id     TEXT,
    target_peer_type   TEXT
);
CREATE TABLE IF NOT EXISTS cron_runs (
    id          TEXT PRIMARY KEY,
    job_id      TEXT NOT NULL,
    session_id  TEXT,
    status      TEXT,
    error       TEXT,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER
);
`

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// modernc.org/sqlite gives each connection its own private in-memory
	// database when using ":memory:". Pin to a single connection so the test
	// goroutines (StartRun in fire(), ListRuns in the test goroutine, ...)
	// all observe the same rows.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(cronSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fakeDispatcher records all DispatchCronJob calls and can return canned errors.
type fakeDispatcher struct {
	mu        sync.Mutex
	calls     []dispatchCall
	wantErr   error
	sessionID string
	fired     chan struct{}
	once      sync.Once
}

type dispatchCall struct {
	AgentID, ProjectID, Prompt           string
	ChannelID, PeerID, PeerType          string
	CtxCanceled                          bool
}

func newFakeDispatcher(sessionID string) *fakeDispatcher {
	return &fakeDispatcher{sessionID: sessionID, fired: make(chan struct{}, 1)}
}

func (f *fakeDispatcher) DispatchCronJob(
	ctx context.Context,
	agentID, projectID, prompt string,
	channelID, peerID, peerType string,
) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, dispatchCall{
		AgentID: agentID, ProjectID: projectID, Prompt: prompt,
		ChannelID: channelID, PeerID: peerID, PeerType: peerType,
		CtxCanceled: ctx.Err() != nil,
	})
	f.mu.Unlock()
	f.once.Do(func() { close(f.fired) })
	return f.sessionID, f.wantErr
}

func (f *fakeDispatcher) Calls() []dispatchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]dispatchCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// nopBus is a minimal Bus that just counts publishes; avoids needing Start().
type nopBus struct {
	count atomic.Int64
	last  atomic.Pointer[string]
}

func (b *nopBus) Publish(topic string, _ any) {
	b.count.Add(1)
	t := topic
	b.last.Store(&t)
}
func (b *nopBus) Subscribe(_ ...string) (<-chan bus.Envelope, func()) {
	ch := make(chan bus.Envelope)
	return ch, func() {}
}
func (b *nopBus) Start(_ context.Context)              {}
func (b *nopBus) Drain(_ time.Duration) error          { return nil }

// ── tests ─────────────────────────────────────────────────────────────────────

func TestAddJob_Success(t *testing.T) {
	db := newTestDB(t)
	disp := newFakeDispatcher("sess-1")
	s := New(db, &nopBus{}, disp)

	created, err := s.AddJob(context.Background(), Job{
		Name:    "hello",
		Spec:    "@every 1h",
		AgentID: "agent-1",
		Prompt:  "say hi",
	})
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected generated ID")
	}
	if !created.Enabled {
		t.Fatal("CreateJob should default enabled=true")
	}

	jobs, err := s.ListJobs(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobs: %v len=%d", err, len(jobs))
	}
	if _, ok := s.entryMap[created.ID]; !ok {
		t.Fatal("entryMap should contain new job")
	}
}

func TestAddJob_InvalidSpecRollsBackDB(t *testing.T) {
	db := newTestDB(t)
	s := New(db, &nopBus{}, newFakeDispatcher(""))

	_, err := s.AddJob(context.Background(), Job{
		Name: "bad", Spec: "not a cron", AgentID: "a", Prompt: "p",
	})
	if err == nil {
		t.Fatal("expected error for invalid spec")
	}

	// DB row should have been rolled back
	jobs, _ := s.ListJobs(context.Background())
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs after rollback, got %d", len(jobs))
	}
	if len(s.entryMap) != 0 {
		t.Fatalf("expected empty entryMap, got %d", len(s.entryMap))
	}
}

func TestDeleteJob_RemovesEntryAndRow(t *testing.T) {
	db := newTestDB(t)
	s := New(db, &nopBus{}, newFakeDispatcher(""))

	created, err := s.AddJob(context.Background(), Job{
		Name: "x", Spec: "@every 1h", AgentID: "a", Prompt: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteJob(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	if _, ok := s.entryMap[created.ID]; ok {
		t.Fatal("entryMap should not contain deleted job")
	}
	jobs, _ := s.ListJobs(context.Background())
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(jobs))
	}
}

func TestToggleJob_DisableThenEnable(t *testing.T) {
	db := newTestDB(t)
	s := New(db, &nopBus{}, newFakeDispatcher(""))

	created, err := s.AddJob(context.Background(), Job{
		Name: "x", Spec: "@every 1h", AgentID: "a", Prompt: "p",
	})
	if err != nil {
		t.Fatal(err)
	}

	// disable
	if err := s.ToggleJob(context.Background(), created.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, ok := s.entryMap[created.ID]; ok {
		t.Fatal("entryMap should drop entry after disable")
	}
	j, _ := s.getJob(context.Background(), created.ID)
	if j.Enabled {
		t.Fatal("DB should reflect enabled=false")
	}

	// enable again
	if err := s.ToggleJob(context.Background(), created.ID, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, ok := s.entryMap[created.ID]; !ok {
		t.Fatal("entryMap should contain entry after enable")
	}
}

func TestToggleJob_EnableInvalidSpec_RollsBack(t *testing.T) {
	db := newTestDB(t)
	s := New(db, &nopBus{}, newFakeDispatcher(""))

	// Insert directly via repo so that we can store an invalid-but-disabled spec.
	created, err := s.repo.CreateJob(context.Background(), Job{
		Name: "x", Spec: "garbage", AgentID: "a", Prompt: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Job is created enabled=true by repo; flip to disabled to mirror "imported but off".
	_ = s.repo.SetEnabled(context.Background(), created.ID, false)

	err = s.ToggleJob(context.Background(), created.ID, true)
	if err == nil {
		t.Fatal("expected error toggling job with invalid spec")
	}
	// enabled flag must have been rolled back to false
	j, _ := s.getJob(context.Background(), created.ID)
	if j.Enabled {
		t.Fatal("enabled flag should be rolled back on addEntry failure")
	}
}

func TestRunNow_FiresDispatcherAndRecordsRun(t *testing.T) {
	db := newTestDB(t)
	disp := newFakeDispatcher("session-xyz")
	b := &nopBus{}
	s := New(db, b, disp)

	created, err := s.AddJob(context.Background(), Job{
		Name: "x", Spec: "@every 1h", AgentID: "agent-1",
		ProjectID: "proj-1", Prompt: "go",
		TargetChannelID: "ch1", TargetPeerID: "peer1", TargetPeerType: "user",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	select {
	case <-disp.fired:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher was not invoked in time")
	}

	calls := disp.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	c := calls[0]
	if c.AgentID != "agent-1" || c.ProjectID != "proj-1" || c.Prompt != "go" {
		t.Errorf("unexpected dispatch call: %+v", c)
	}
	if c.ChannelID != "ch1" || c.PeerID != "peer1" || c.PeerType != "user" {
		t.Errorf("target peer triple lost: %+v", c)
	}

	// The fire goroutine writes the run record async; poll briefly.
	var runs []RunRecord
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runs, _ = s.ListRuns(context.Background(), created.ID, 10)
		if len(runs) == 1 && runs[0].FinishedAt != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Status != "ok" {
		t.Errorf("expected status=ok, got %q (err=%q)", runs[0].Status, runs[0].Error)
	}
	if runs[0].SessionID != "session-xyz" {
		t.Errorf("session id not propagated: %q", runs[0].SessionID)
	}
	if b.count.Load() == 0 {
		t.Errorf("expected at least one bus.Publish, got 0")
	}
}

func TestRunNow_DispatcherError_RecordedAsFailed(t *testing.T) {
	db := newTestDB(t)
	disp := newFakeDispatcher("")
	disp.wantErr = errors.New("boom")
	s := New(db, &nopBus{}, disp)

	created, _ := s.AddJob(context.Background(), Job{
		Name: "x", Spec: "@every 1h", AgentID: "a", Prompt: "p",
	})
	if err := s.RunNow(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}

	select {
	case <-disp.fired:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher not invoked")
	}

	var runs []RunRecord
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runs, _ = s.ListRuns(context.Background(), created.ID, 10)
		if len(runs) == 1 && runs[0].FinishedAt != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d (jobID=%s)", len(runs), created.ID)
	}
	if runs[0].Status != "failed" {
		t.Errorf("expected failed, got %q", runs[0].Status)
	}
	if runs[0].Error != "boom" {
		t.Errorf("expected error message propagated, got %q", runs[0].Error)
	}
}

func TestStart_LoadsEnabledOnly(t *testing.T) {
	db := newTestDB(t)
	r := repo.NewCronRepo(db)
	enabled, _ := r.CreateJob(context.Background(), Job{
		Name: "on", Spec: "@every 1h", AgentID: "a", Prompt: "p",
	})
	disabled, _ := r.CreateJob(context.Background(), Job{
		Name: "off", Spec: "@every 1h", AgentID: "a", Prompt: "p",
	})
	_ = r.SetEnabled(context.Background(), disabled.ID, false)
	// Also seed a row with invalid spec to ensure Start() does not panic.
	bad, _ := r.CreateJob(context.Background(), Job{
		Name: "bad", Spec: "garbage", AgentID: "a", Prompt: "p",
	})

	s := New(db, &nopBus{}, newFakeDispatcher(""))
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	if _, ok := s.entryMap[enabled.ID]; !ok {
		t.Error("enabled job should be registered")
	}
	if _, ok := s.entryMap[disabled.ID]; ok {
		t.Error("disabled job should not be registered")
	}
	if _, ok := s.entryMap[bad.ID]; ok {
		t.Error("invalid-spec job must not be registered (warning only)")
	}
}

func TestStart_CapturesContextForFire(t *testing.T) {
	db := newTestDB(t)
	disp := newFakeDispatcher("s")
	s := New(db, &nopBus{}, disp)

	created, _ := s.AddJob(context.Background(), Job{
		Name: "x", Spec: "@every 1h", AgentID: "a", Prompt: "p",
	})

	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	cancel() // cancel the root ctx before firing

	if err := s.RunNow(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}

	select {
	case <-disp.fired:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher not invoked")
	}
	calls := disp.Calls()
	if len(calls) != 1 || !calls[0].CtxCanceled {
		t.Fatalf("expected ctx to be canceled inside fire, calls=%+v", calls)
	}
}
