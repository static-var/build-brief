package buildbrief_test

import (
	"os"
	"strings"
	"testing"
)

func TestWebsiteDoesNotAdvertiseRemovedDaemonMode(t *testing.T) {
	content := readRepositoryText(t, "site/index.html")
	if strings.Contains(content, "--daemon-mode") {
		t.Fatal("website must not advertise the unsupported --daemon-mode flag")
	}
}

func TestPlanIsMarkedHistoricalAtTop(t *testing.T) {
	content := readRepositoryText(t, "plan.md")
	const notice = "# Historical planning document\n\n> **Historical document:** This is the original planning record. For current usage and behavior, see the [README](./README.md) and the current code."
	if !strings.HasPrefix(content, notice) {
		t.Fatalf("plan.md must begin with the historical-document notice, got %q", content[:min(len(content), len(notice))])
	}
}

func TestRepositoryAgentInstructionsCoverSafetyAndChecks(t *testing.T) {
	content := readRepositoryText(t, "AGENTS.md")
	for _, required := range []string{
		"go test ./...",
		"go vet ./...",
		"go test -race ./...",
		"Do not publish, merge, or release without explicit approval.",
		"Local-only: gains history stays on this machine; no gains data is transmitted.",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("AGENTS.md must contain %q", required)
		}
	}
}

func readRepositoryText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
