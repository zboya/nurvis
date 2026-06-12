package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zboya/nurvis/internal/provider"
)

// SkillProvider is the minimal dependency interface of the use_skill tool on the skill package.
// Implemented by internal/skill.Manager; an interface is used here to avoid the tools package
// reverse-depending on the skill package.
type SkillProvider interface {
	// LookupForAgent checks whether a skill name is visible to the specified agent,
	// returning (granted, info, ok).
	LookupForAgent(ctx context.Context, agentID, skillName string) (granted bool, info SkillInfo, ok bool)
}

// SkillInfo is the skill metadata needed when use_skill returns results to the model.
type SkillInfo struct {
	Name        string
	Description string
	Body        string // SKILL.md content
	Dir         string // Absolute path of the skill directory
}

// UseSkill is a built-in tool that "expands skill instructions on demand".
//
// After the model calls use_skill(name="<skill-name>"), the tool returns the skill's SKILL.md
// full content along with a listing of scripts/ references/ assets/ resources under the directory,
// so the model can proceed using general exec / fs tools. This follows the "progressive disclosure"
// paradigm of Anthropic Agent Skills.
type UseSkill struct {
	provider SkillProvider
}

// NewUseSkill creates a use_skill tool instance.
func NewUseSkill(p SkillProvider) *UseSkill { return &UseSkill{provider: p} }

func (*UseSkill) Name() string { return "use_skill" }

func (*UseSkill) Description() string {
	return "Load the full instructions of a skill by name. " +
		"Skills provide specialized capabilities and domain knowledge. " +
		"Call this when the user's task matches an entry in <available_skills>; " +
		"the returned SKILL.md will guide you on how to complete the task " +
		"(typically by running scripts in the skill directory via the exec tool)."
}

func (*UseSkill) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "use_skill",
		Description: "Load the full SKILL.md instructions of a skill by its name (no arguments other than name).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The skill name as listed in <available_skills>.",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (u *UseSkill) Invoke(ctx context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	var args struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &args)
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return &Result{Content: "argument 'name' is required", IsError: true}, nil
	}
	if u.provider == nil {
		return &Result{Content: "skill provider not configured", IsError: true}, nil
	}

	granted, info, ok := u.provider.LookupForAgent(ctx, scope.AgentID, name)
	if !ok {
		return &Result{
			Content: fmt.Sprintf("skill %q not found", name),
			IsError: true,
		}, nil
	}
	if !granted {
		return &Result{
			Content: fmt.Sprintf("skill %q is not granted to this agent", name),
			IsError: true,
		}, nil
	}

	// Register the skill directory in Scope so subsequent exec calls can access it via environment variables;
	// note that Scope is passed by value, so we only register when writable (map is non-nil).
	if scope.SkillRoots != nil {
		scope.SkillRoots[info.Name] = info.Dir
	}

	listing := summarizeSkillDir(info.Dir)

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Skill: %s\n\n", info.Name)
	if info.Description != "" {
		fmt.Fprintf(&sb, "%s\n\n", info.Description)
	}
	fmt.Fprintf(&sb, "Skill directory: %s\n", info.Dir)
	fmt.Fprintf(&sb, "Use the `exec` tool with `cd %s && ...` (or read files via `read_file`) "+
		"to invoke scripts and references inside this skill.\n\n", info.Dir)
	if listing != "" {
		fmt.Fprintf(&sb, "## Resources\n%s\n\n", listing)
	}
	fmt.Fprintf(&sb, "## SKILL.md\n\n%s\n", strings.TrimSpace(info.Body))

	return &Result{Content: sb.String()}, nil
}

// summarizeSkillDir lists first-level files under scripts / references / assets in the skill directory.
// Does not recurse to avoid excessive output; the model can use list_files to explore further if needed.
func summarizeSkillDir(dir string) string {
	subs := []string{"scripts", "references", "assets"}
	var sb strings.Builder
	for _, sub := range subs {
		full := filepath.Join(dir, sub)
		entries, err := os.ReadDir(full)
		if err != nil || len(entries) == 0 {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() {
				n += "/"
			}
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Fprintf(&sb, "- %s/: %s\n", sub, strings.Join(names, ", "))
	}
	return sb.String()
}
