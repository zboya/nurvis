package agent

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/zboya/nurvis/internal/provider"
)

const (
	sysPrompt = `You are an AI assistant helping users accomplish various tasks.
Here are some guiding principles:

- **MUST respond in the same language the user uses.**
- **Your goal is to complete the task in <task>.**

## Tool Use Rules

1. **Keep calling tools until the task is fully done.** If completing the task requires external tools (file operations, commands, searches, etc.), you MUST continue invoking tools until the task is actually finished. Do NOT stop and produce a final text reply while the task is still in progress.
2. **One tool at a time when order matters.** When a later step depends on the result of an earlier one, wait for the result before proceeding.
3. **Stop calling tools only when done.** You may give a final answer without calling any tool only when the task is complete or when you have determined (with evidence) that the task cannot be completed.

`
)

// buildSystemMessage constructs the system message content for the current round.
// It can be rebuilt at the start of each round to inject the latest workspace
// path, time, session summary, etc.
func (l *Loop) buildSystemMessage(state *RunState) provider.Message {
	sb := &strings.Builder{}
	sb.WriteString(sysPrompt)
	fmt.Fprintf(sb, `
<agent>
Name: %s
Description: %s
</agent>

<info>
Current workspace: %s
OS: %s
ARCH: %s
Time: %s
</info>
`, l.agent.Name, l.agent.SystemPrompt, state.Workspace,
		runtime.GOOS, runtime.GOARCH, time.Now().Format("2006-01-02 15:04:05"))

	if state.Summary != "" {
		fmt.Fprintf(sb, "\n<conversation_summary>\n%s\n</conversation_summary>\n", state.Summary)
	}

	// Inject the list of skills authorized for the current agent (name+description only, progressive disclosure).
	// The model decides whether to call use_skill to load full instructions based on this.
	if l.skillMgr != nil {
		skills, err := l.skillMgr.ListForAgent(context.Background(), l.agent.ID)
		if err == nil && len(skills) > 0 {
			fmt.Fprintf(sb, `## Skill Use Rules

Skills provide specialized capabilities and domain knowledge as packaged instruction sets.
When the user's task matches an entry in <available_skills>, call the `+"`use_skill`"+` tool with the skill name to load its full SKILL.md instructions, then follow them (typically by running scripts in the skill directory via the `+"`exec`"+` tool, or reading reference files via `+"`read_file`"+`). Do NOT guess what a skill does from its short description — always load it first.`)
			fmt.Fprint(sb, "\n<available_skills>\n")
			for _, s := range skills {
				if s.Description == "" {
					fmt.Fprintf(sb, "- %s\n", s.Name)
				} else {
					fmt.Fprintf(sb, "- %s: %s\n", s.Name, s.Description)
				}
			}
			fmt.Fprint(sb, "</available_skills>\n")
		}
	}

	// Passive-reply scenario (message arrived via QQ/WeChat Channel): must explicitly
	// call channel.send to reply to the user. The target peer is automatically determined
	// by the system from the current context; the model only needs to provide text and/or media.
	if state.ChannelInstanceID != "" && state.ReplyTo != nil {
		fmt.Fprintf(sb, `
<channel_reply>
This conversation arrived via the "%s" channel. To respond to the user, call the `+"`channel.send`"+` tool with `+"`text`"+` (and/or `+"`media`"+`).
Plain text in your final reply will NOT reach the user; only `+"`channel.send`"+` calls do.
</channel_reply>
`, state.Channel)
	}

	return provider.Message{Role: provider.RoleSystem, Content: sb.String()}
}
