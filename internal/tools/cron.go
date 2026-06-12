package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zboya/nurvis/internal/provider"
	"github.com/zboya/nurvis/internal/store/repo"
)

// CronManager is the minimal scheduler facet the tools layer depends on,
// avoiding a tools → scheduler reverse dependency. The app wiring layer
// adapts *scheduler.Scheduler into it (the existing methods already match
// these signatures, so a thin adapter is enough).
type CronManager interface {
	AddJob(ctx context.Context, j repo.Job) (*repo.Job, error)
	DeleteJob(ctx context.Context, id string) error
	ToggleJob(ctx context.Context, id string, enabled bool) error
	ListJobs(ctx context.Context) ([]repo.Job, error)
	ListRuns(ctx context.Context, jobID string, limit int) ([]repo.RunRecord, error)
	RunNow(ctx context.Context, id string) error
}

// CronTool is the unified entry point for scheduled-task management.
//
// Modeled after the "scheduled tasks" capability shipped by mainstream agents
// (Claude / ChatGPT / OpenClaw): a single tool with an `action` selector
// covers create / list / delete / toggle / run_now / runs.
//
// Default behavior:
//   - `agent_id` falls back to the current agent (from Scope) when omitted.
//   - `project_id` falls back to the current project when omitted.
//   - In passive-response scenarios (e.g. triggered by a QQ/WeChat inbound),
//     the channel/peer triple is auto-inherited from Scope, so the model only
//     needs to decide *what* to schedule — the reply target is preserved.
//
// The cron expression itself is the model's responsibility: the tool only
// accepts a robfig/cron-compatible spec (with seconds, e.g. "0 30 9 * * *"
// for 9:30 every day). The tool does not parse natural language — the model
// should translate user intent into a cron spec before calling.
type CronTool struct {
	mgr CronManager
}

// NewCronTool builds the tool. When mgr is nil, every Invoke returns an error
// (keeping registry registration shape consistent with optional dependencies).
func NewCronTool(mgr CronManager) *CronTool { return &CronTool{mgr: mgr} }

func (*CronTool) Name() string { return "cron" }

func (*CronTool) Description() string {
	return "Manage scheduled tasks (create / list / delete / toggle / run_now / runs) " +
		"that fire a new Agent Loop on a cron schedule with a preset prompt."
}

func (*CronTool) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name: "cron",
		Description: strings.TrimSpace(`
Manage scheduled tasks. Each task wakes an Agent on a cron schedule and
delivers a preset prompt as if the user had just sent it.

Use this tool when the user says things like:
  "提醒我每天早上 9 点汇总昨日新闻"
  "每周一早上检查一遍服务器状态"
  "10 分钟后给我发个总结"

Cron spec uses 6 fields with seconds: "sec min hour dom mon dow"
  Examples:
    "0 30 9 * * *"      every day at 09:30
    "0 0 9 * * MON"     every Monday at 09:00
    "0 */15 * * * *"    every 15 minutes
    "0 0 9 1 * *"       9 AM on the 1st of every month

Actions:
  - create:    create a new job. Required: spec, prompt, name.
  - list:      list all jobs. No args.
  - delete:    remove a job. Required: id.
  - toggle:    enable / disable a job. Required: id, enabled.
  - run_now:   fire a job immediately, bypassing the schedule. Required: id.
  - runs:      list recent run history of a job. Required: id. Optional: limit.
`),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Operation to perform.",
					"enum":        []string{"create", "list", "delete", "toggle", "run_now", "runs"},
				},
				"id": map[string]any{
					"type":        "string",
					"description": "Job ID. Required for delete / toggle / run_now / runs.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Human-readable task name (create only).",
				},
				"spec": map[string]any{
					"type":        "string",
					"description": "robfig/cron expression with seconds, e.g. \"0 30 9 * * *\" (create only).",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Prompt delivered to the Agent when the task fires (create only).",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Target Agent ID. Defaults to the current Agent when omitted (create only).",
				},
				"project_id": map[string]any{
					"type":        "string",
					"description": "Workspace / project ID. Defaults to the current project when omitted (create only).",
				},
				"enabled": map[string]any{
					"type":        "boolean",
					"description": "true to enable, false to disable (toggle only).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max number of run records to return (runs only). Defaults to 20.",
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t *CronTool) Invoke(ctx context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	if t.mgr == nil {
		return errResult("cron: scheduler unavailable in this build"), nil
	}
	var args struct {
		Action    string `json:"action"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		Spec      string `json:"spec"`
		Prompt    string `json:"prompt"`
		AgentID   string `json:"agent_id"`
		ProjectID string `json:"project_id"`
		Enabled   *bool  `json:"enabled"`
		Limit     int    `json:"limit"`
	}
	_ = json.Unmarshal(raw, &args)
	action := strings.ToLower(strings.TrimSpace(args.Action))

	switch action {
	case "create":
		return t.create(ctx, args.Name, args.Spec, args.Prompt, args.AgentID, args.ProjectID, scope)
	case "list":
		return t.list(ctx)
	case "delete":
		return t.delete(ctx, args.ID)
	case "toggle":
		if args.Enabled == nil {
			return errResult("cron.toggle: enabled (bool) is required"), nil
		}
		return t.toggle(ctx, args.ID, *args.Enabled)
	case "run_now", "run-now", "runnow":
		return t.runNow(ctx, args.ID)
	case "runs":
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		return t.runs(ctx, args.ID, limit)
	default:
		return errResult(fmt.Sprintf("cron: unknown action %q", action)), nil
	}
}

// ── action handlers ────────────────────────────────────────────────────────────

func (t *CronTool) create(ctx context.Context, name, spec, prompt, agentID, projectID string, scope Scope) (*Result, error) {
	name = strings.TrimSpace(name)
	spec = strings.TrimSpace(spec)
	prompt = strings.TrimSpace(prompt)
	if name == "" || spec == "" || prompt == "" {
		return errResult("cron.create: name / spec / prompt are all required"), nil
	}

	if agentID == "" {
		agentID = scope.AgentID
	}
	if agentID == "" {
		return errResult("cron.create: agent_id is required (no agent in current scope)"), nil
	}

	if projectID == "" {
		projectID = scope.ProjectID
	}

	job := repo.Job{
		Name:      name,
		Spec:      spec,
		AgentID:   agentID,
		ProjectID: projectID,
		Prompt:    prompt,
		Enabled:   true,
	}
	// Inherit channel binding from current scope so cron-triggered loops can
	// reply back to the original peer (QQ / WeChat) automatically.
	if scope.ChannelID != "" && scope.ReplyTo != nil && scope.ReplyTo.ID != "" {
		job.TargetChannelID = scope.ChannelID
		job.TargetPeerID = scope.ReplyTo.ID
		job.TargetPeerType = strings.ToLower(scope.ReplyTo.Type)
		if job.TargetPeerType == "" {
			job.TargetPeerType = "user"
		}
	}

	created, err := t.mgr.AddJob(ctx, job)
	if err != nil {
		return errResult(fmt.Sprintf("cron.create: %v", err)), nil
	}
	return jsonResult(map[string]any{
		"ok":         true,
		"id":         created.ID,
		"name":       created.Name,
		"spec":       created.Spec,
		"agent_id":   created.AgentID,
		"project_id": created.ProjectID,
		"enabled":    created.Enabled,
	})
}

func (t *CronTool) list(ctx context.Context) (*Result, error) {
	jobs, err := t.mgr.ListJobs(ctx)
	if err != nil {
		return errResult(fmt.Sprintf("cron.list: %v", err)), nil
	}
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, map[string]any{
			"id":         j.ID,
			"name":       j.Name,
			"spec":       j.Spec,
			"agent_id":   j.AgentID,
			"project_id": j.ProjectID,
			"prompt":     j.Prompt,
			"enabled":    j.Enabled,
		})
	}
	return jsonResult(map[string]any{"count": len(out), "jobs": out})
}

func (t *CronTool) delete(ctx context.Context, id string) (*Result, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return errResult("cron.delete: id is required"), nil
	}
	if err := t.mgr.DeleteJob(ctx, id); err != nil {
		return errResult(fmt.Sprintf("cron.delete: %v", err)), nil
	}
	return jsonResult(map[string]any{"ok": true, "id": id})
}

func (t *CronTool) toggle(ctx context.Context, id string, enabled bool) (*Result, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return errResult("cron.toggle: id is required"), nil
	}
	if err := t.mgr.ToggleJob(ctx, id, enabled); err != nil {
		return errResult(fmt.Sprintf("cron.toggle: %v", err)), nil
	}
	return jsonResult(map[string]any{"ok": true, "id": id, "enabled": enabled})
}

func (t *CronTool) runNow(ctx context.Context, id string) (*Result, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return errResult("cron.run_now: id is required"), nil
	}
	if err := t.mgr.RunNow(ctx, id); err != nil {
		return errResult(fmt.Sprintf("cron.run_now: %v", err)), nil
	}
	return jsonResult(map[string]any{"ok": true, "id": id, "fired_at": time.Now().Format(time.RFC3339)})
}

func (t *CronTool) runs(ctx context.Context, id string, limit int) (*Result, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return errResult("cron.runs: id is required"), nil
	}
	records, err := t.mgr.ListRuns(ctx, id, limit)
	if err != nil {
		return errResult(fmt.Sprintf("cron.runs: %v", err)), nil
	}
	out := make([]map[string]any, 0, len(records))
	for _, r := range records {
		item := map[string]any{
			"id":         r.ID,
			"job_id":     r.JobID,
			"session_id": r.SessionID,
			"status":     r.Status,
			"error":      r.Error,
			"started_at": r.StartedAt.Format(time.RFC3339),
		}
		if r.FinishedAt != nil {
			item["finished_at"] = r.FinishedAt.Format(time.RFC3339)
		}
		out = append(out, item)
	}
	return jsonResult(map[string]any{"count": len(out), "runs": out})
}

// ── helpers ────────────────────────────────────────────────────────────────────

func errResult(msg string) *Result {
	return &Result{Content: msg, IsError: true}
}

func jsonResult(payload any) (*Result, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return errResult(fmt.Sprintf("cron: marshal result: %v", err)), nil
	}
	return &Result{Content: string(b)}, nil
}
