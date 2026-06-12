package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/zboya/nurvis/internal/provider"
)

// Exec runs a shell command within the workspace directory.
type Exec struct{}

func (*Exec) Name() string { return "exec" }
func (*Exec) Description() string {
	return "Execute a shell command within the current workspace directory."
}
func (*Exec) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "exec",
		Description: "Execute a shell command within the current workspace directory. Returns stdout and stderr.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute. On Unix/macOS runs via bash -c; on Windows runs via PowerShell.",
				},
				"timeout_seconds": map[string]any{
					"type":        "number",
					"description": "Maximum execution time in seconds (default 30, max 120).",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (*Exec) Invoke(ctx context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Command        string  `json:"command"`
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}
	_ = json.Unmarshal(raw, &args)
	if args.Command == "" {
		return &Result{Content: "command is required", IsError: true}, nil
	}
	timeoutSec := args.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if timeoutSec > 120 {
		timeoutSec = 120
	}

	timeout := time.Duration(timeoutSec * float64(time.Second))
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	wsDir := scope.WorkspaceDir
	if wsDir == "" {
		wsDir = "."
	}

	cmd := buildShellCmd(execCtx, args.Command)
	cmd.Dir = wsDir

	// Pass activated skill directories as environment variables to the child process,
	// allowing skill scripts to reference paths relatively.
	if len(scope.SkillRoots) > 0 {
		env := append([]string{}, os.Environ()...)
		for name, dir := range scope.SkillRoots {
			env = append(env, fmt.Sprintf("NURVIS_SKILL_%s_DIR=%s",
				strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name)), dir))
		}
		cmd.Env = env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var sb strings.Builder
	if stdout.Len() > 0 {
		sb.WriteString("stdout:\n")
		sb.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("stderr:\n")
		sb.WriteString(stderr.String())
	}
	if sb.Len() == 0 {
		sb.WriteString("(no output)")
	}

	isError := false
	if err != nil {
		sb.WriteString(fmt.Sprintf("\nexit error: %v", err))
		isError = true
	}

	// Limit output length (prevent overflowing the context window)
	output := sb.String()
	const maxLen = 8192
	if len(output) > maxLen {
		output = output[:maxLen] + fmt.Sprintf("\n... (truncated, total %d bytes)", len(output))
	}

	return &Result{Content: output, IsError: isError}, nil
}

// buildShellCmd selects the appropriate shell for the current OS.
// Uses PowerShell on Windows, bash (fallback to sh) on Unix systems.
func buildShellCmd(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	}
	// macOS / Linux: prefer bash, fallback to sh if not found
	shell := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		shell = "sh"
	}
	return exec.CommandContext(ctx, shell, "-c", command)
}
