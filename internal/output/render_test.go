package output

import (
	"bytes"
	"strings"
	"testing"

	"build-brief/internal/reducer"
)

func TestRenderHumanKeepsSuccessConcise(t *testing.T) {
	summary := reducer.Summary{
		Success:         true,
		BuildStatusLine: "BUILD SUCCESSFUL in 5s",
		CommandLine:     "gradle test",
		RawLogPath:      "/tmp/raw.log",
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render success output: %v", err)
	}

	rendered := out.String()
	if strings.TrimSpace(rendered) != "BUILD SUCCESSFUL in 5s" {
		t.Fatalf("unexpected success output %q", rendered)
	}
}

func TestRenderHumanKeepsFailureDetails(t *testing.T) {
	summary := reducer.Summary{
		Success:         false,
		BuildStatusLine: "BUILD FAILED in 245ms",
		CommandLine:     "gradle smokeSymbolCompile",
		RawLogPath:      "/tmp/raw.log",
		FailedTasks:     []string{":smokeSymbolCompile"},
		ImportantLines: []string{
			"BUILD FAILED in 245ms",
			"Execution failed for task ':smokeSymbolCompile'.",
		},
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render failure output: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"BUILD FAILED in 245ms",
		"Command: gradle smokeSymbolCompile",
		"Failed tasks:",
		"Execution failed for task ':smokeSymbolCompile'.",
		"Raw log: /tmp/raw.log",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected failure output to contain %q, got %q", expected, rendered)
		}
	}

	if strings.Count(rendered, "BUILD FAILED in 245ms") != 1 {
		t.Fatalf("expected build status line once, got %q", rendered)
	}
}
