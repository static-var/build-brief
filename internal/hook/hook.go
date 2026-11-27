// Package hook implements the PreToolUse guardrail that agent plugins run
// before each shell command. It reads the agent's hook payload from stdin and,
// when the command is a routine Gradle invocation, blocks it and suggests the
// build-brief equivalent using the agent's own decision format.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"build-brief/internal/rewrite"
)

// Agent names accepted by Run, as used in `build-brief hook <agent>`.
const (
	Claude  = "claude"
	Codex   = "codex"
	Copilot = "copilot"
)

const denyReason = "Routine Gradle command or chain intercepted by build-brief. Use: "

// Agents lists the supported agent names.
func Agents() []string {
	return []string{Claude, Codex, Copilot}
}

// Run handles one hook invocation for agent and returns the process exit code.
// Payloads that cannot be parsed, other tools, and commands without a routine
// Gradle segment are allowed silently, so a hook never blocks unrelated work.
func Run(agent string, stdin io.Reader, stdout, stderr io.Writer) int {
	var parse func([]byte) (string, bool)
	switch agent {
	case Claude:
		parse = claudeCommand
	case Codex:
		parse = codexCommand
	case Copilot:
		parse = copilotCommand
	default:
		fmt.Fprintf(stderr, "build-brief: hook requires one of: %s\n", strings.Join(Agents(), ", "))
		return 2
	}

	payload, err := io.ReadAll(stdin)
	if err != nil {
		return 0
	}
	command, ok := parse(payload)
	if !ok || strings.TrimSpace(command) == "" {
		return 0
	}
	rewritten, changed := rewrite.ShellCommand(command)
	if !changed {
		return 0
	}

	switch agent {
	case Claude:
		writeJSON(stdout, map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":            "PreToolUse",
				"permissionDecision":       "deny",
				"permissionDecisionReason": denyReason + rewritten,
			},
		})
	case Copilot:
		writeJSON(stdout, map[string]any{
			"permissionDecision":       "deny",
			"permissionDecisionReason": denyReason + rewritten,
		})
	case Codex:
		// Codex blocks the tool call on exit code 2 and shows stderr to the agent.
		fmt.Fprintf(stderr, "[build-brief] Codex blocked a routine Gradle command or chain.\n"+
			"[build-brief] Use this instead:\n\n"+
			"  %s\n\n"+
			"This Codex PreToolUse hook can block and suggest a safer replacement,\n"+
			"but it cannot transparently rewrite and continue in place.\n", rewritten)
		return 2
	}
	return 0
}

func claudeCommand(payload []byte) (string, bool) {
	var input struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(payload, &input) != nil || input.ToolName != "Bash" {
		return "", false
	}
	return input.ToolInput.Command, true
}

// codexCommand needs no tool name check: the plugin's hook matcher already
// limits it to Bash.
func codexCommand(payload []byte) (string, bool) {
	var input struct {
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(payload, &input) != nil {
		return "", false
	}
	return input.ToolInput.Command, true
}

// copilotCommand reads the command from toolArgs, which Copilot sends as a
// JSON-encoded string rather than an object.
func copilotCommand(payload []byte) (string, bool) {
	var input struct {
		ToolName string `json:"toolName"`
		ToolArgs string `json:"toolArgs"`
	}
	if json.Unmarshal(payload, &input) != nil || !strings.EqualFold(input.ToolName, "bash") {
		return "", false
	}
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(input.ToolArgs), &args) != nil {
		return "", false
	}
	return args.Command, true
}

func writeJSON(w io.Writer, value any) {
	encoder := json.NewEncoder(w)
	// Keep shell operators such as && readable instead of escaping them as \u0026.
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
