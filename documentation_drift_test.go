package buildbrief_test

import (
	"os"
	"strings"
	"testing"
)

func TestPlanHasHistoricalContextAndCurrentReadmeLink(t *testing.T) {
	content := readRepositoryText(t, "plan.md")
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") || !strings.Contains(strings.ToLower(line), "historical") {
			t.Fatalf("plan.md must begin with a historical heading, got %q", line)
		}
		break
	}
	if !strings.Contains(content, "](./README.md)") {
		t.Fatal("plan.md must link readers to the current README")
	}
}

func TestRepositoryAgentInstructionsCoverRequiredConcepts(t *testing.T) {
	content := readRepositoryText(t, "AGENTS.md")
	requireContains(t, content, "go test ./...", "go vet ./...", "go test -race ./...")

	lower := strings.ToLower(content)
	requireContains(t, lower, "gains", "local", "transmitted")
	if !containsAny(lower, "publish", "merge", "release") || !strings.Contains(lower, "approval") {
		t.Fatal("AGENTS.md must require approval for release-related actions")
	}
}

func TestWebsiteDocumentsActualDaemonBehavior(t *testing.T) {
	content := readRepositoryText(t, "site/index.html")
	lower := strings.ToLower(content)
	requireDaemonStatement(t, websiteSection(t, lower, "<section id=\"limitations\"", "</section>"))
	requireDaemonStatement(t, websiteSection(t, lower, "what about the gradle daemon?", "</details>"))

	for _, forbidden := range []string{
		"--daemon-mode on",
		"--daemon-mode off",
		"daemon mode defaults to",
		"after <code>--</code>",
		"via <code>--</code>",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("website must not claim %q", forbidden)
		}
	}
}

func requireDaemonStatement(t *testing.T, statement string) {
	t.Helper()
	requireContains(t, statement, "--daemon", "--no-daemon", "gradle", "default", "--daemon-mode", "unsupported")
}

func websiteSection(t *testing.T, content, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(content, startMarker)
	if start < 0 {
		t.Fatalf("website must contain %q", startMarker)
	}
	content = content[start:]
	end := strings.Index(content, endMarker)
	if end < 0 {
		t.Fatalf("website section beginning %q must end with %q", startMarker, endMarker)
	}
	return content[:end]
}

func requireContains(t *testing.T, content string, required ...string) {
	t.Helper()
	for _, term := range required {
		if !strings.Contains(content, term) {
			t.Fatalf("expected documentation concept %q", term)
		}
	}
}

func containsAny(content string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(content, term) {
			return true
		}
	}
	return false
}

func readRepositoryText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
