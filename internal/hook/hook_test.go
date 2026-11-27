package hook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const mixedChain = "build-brief ./gradlew test && ./gradlew check"
const mixedChainRewritten = "build-brief ./gradlew test && build-brief ./gradlew check"

func claudePayload(command any) string {
	return mustJSON(map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}})
}

func codexPayload(command any) string {
	return mustJSON(map[string]any{"tool_input": map[string]any{"command": command}})
}

func copilotPayload(command any) string {
	return mustJSON(map[string]any{"toolName": "bash", "toolArgs": mustJSON(map[string]any{"command": command})})
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func run(agent, payload string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(agent, strings.NewReader(payload), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunDeniesRoutineGradleCommands(t *testing.T) {
	cases := []struct {
		agent, payload string
		decision       func(t *testing.T, stdout []byte) (string, string)
	}{
		{Claude, claudePayload(mixedChain), func(t *testing.T, stdout []byte) (string, string) {
			var output struct {
				HookSpecificOutput struct {
					HookEventName            string `json:"hookEventName"`
					PermissionDecision       string `json:"permissionDecision"`
					PermissionDecisionReason string `json:"permissionDecisionReason"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(stdout, &output); err != nil {
				t.Fatalf("invalid decision JSON: %v: %s", err, stdout)
			}
			if output.HookSpecificOutput.HookEventName != "PreToolUse" {
				t.Fatalf("unexpected hook event name: %s", stdout)
			}
			return output.HookSpecificOutput.PermissionDecision, output.HookSpecificOutput.PermissionDecisionReason
		}},
		{Copilot, copilotPayload(mixedChain), func(t *testing.T, stdout []byte) (string, string) {
			var output struct {
				PermissionDecision       string `json:"permissionDecision"`
				PermissionDecisionReason string `json:"permissionDecisionReason"`
			}
			if err := json.Unmarshal(stdout, &output); err != nil {
				t.Fatalf("invalid decision JSON: %v: %s", err, stdout)
			}
			return output.PermissionDecision, output.PermissionDecisionReason
		}},
	}
	for _, tc := range cases {
		t.Run(tc.agent, func(t *testing.T) {
			code, stdout, stderr := run(tc.agent, tc.payload)
			if code != 0 || stderr != "" {
				t.Fatalf("expected exit 0 without stderr, got %d: %q", code, stderr)
			}
			decision, reason := tc.decision(t, []byte(stdout))
			if decision != "deny" || reason != denyReason+mixedChainRewritten {
				t.Fatalf("unexpected decision %q, reason %q", decision, reason)
			}
			if strings.Contains(stdout, "\\u0026") {
				t.Fatalf("expected readable shell operators, got %s", stdout)
			}
		})
	}

	t.Run(Codex, func(t *testing.T) {
		code, stdout, stderr := run(Codex, codexPayload(mixedChain))
		if code != 2 || stdout != "" {
			t.Fatalf("expected blocking exit 2 without stdout, got %d: %q", code, stdout)
		}
		if !strings.Contains(stderr, "\n  "+mixedChainRewritten+"\n") {
			t.Fatalf("missing replacement in stderr: %q", stderr)
		}
	})
}

func TestRunAllowsEverythingElse(t *testing.T) {
	cases := []struct{ name, agent, payload string }{
		{"claude non-Gradle command", Claude, claudePayload("ls -la")},
		{"claude wrapped command", Claude, claudePayload("build-brief ./gradlew test")},
		{"claude other tool", Claude, `{"tool_name":"Read","tool_input":{"file_path":"build.gradle"}}`},
		{"claude non-string command", Claude, claudePayload(42)},
		{"claude blank command", Claude, claudePayload("  ")},
		{"claude invalid JSON", Claude, "not json"},
		{"claude empty payload", Claude, ""},
		{"codex non-Gradle command", Codex, codexPayload("ls -la")},
		{"codex non-object tool input", Codex, `{"tool_input":"./gradlew test"}`},
		{"copilot non-Gradle command", Copilot, copilotPayload("ls -la")},
		{"copilot other tool", Copilot, `{"toolName":"view","toolArgs":"{\"path\":\"build.gradle\"}"}`},
		{"copilot invalid tool args", Copilot, `{"toolName":"bash","toolArgs":"./gradlew test"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := run(tc.agent, tc.payload)
			if code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("expected silent allow, got %d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestRunAcceptsCopilotToolNameInAnyCase(t *testing.T) {
	payload := mustJSON(map[string]any{"toolName": "Bash", "toolArgs": mustJSON(map[string]any{"command": "./gradlew test"})})
	_, stdout, _ := run(Copilot, payload)
	if !strings.Contains(stdout, `"permissionDecision":"deny"`) {
		t.Fatalf("expected deny decision, got %q", stdout)
	}
}

func TestRunRejectsUnknownAgent(t *testing.T) {
	code, stdout, stderr := run("vim", claudePayload("./gradlew test"))
	if code != 2 || stdout != "" || !strings.Contains(stderr, "claude, codex, copilot") {
		t.Fatalf("expected usage error, got %d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
