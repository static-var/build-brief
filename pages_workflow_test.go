package buildbrief_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestPagesWorkflowPinsActionsToAuditedImmutableRevisions(t *testing.T) {
	content, err := os.ReadFile(".github/workflows/pages.yml")
	if err != nil {
		t.Fatalf("read Pages workflow: %v", err)
	}

	expected := map[string]string{
		"actions/checkout":              "34e114876b0b11c390a56381ad16ebd13914f8d5 # v4.3.1",
		"actions/configure-pages":       "983d7736d9b0ae728b81ab479565c72886d7745b # v5.0.0",
		"actions/upload-pages-artifact": "56afc609e74202658d3ffba0e8f6dda462b719fa # v3.0.1",
		"actions/deploy-pages":          "d6db90164ac5ed86f2b6aed7e0febac5b3c0c03e # v4.0.5",
	}

	usesLine := regexp.MustCompile(`(?m)^\s+uses:\s+([^@\s]+)@([^\s]+)(?:\s+#\s+(.+))?$`)
	matches := usesLine.FindAllStringSubmatch(string(content), -1)
	if len(matches) != len(expected) {
		t.Fatalf("Pages workflow must contain exactly %d action uses, found %d", len(expected), len(matches))
	}

	for _, match := range matches {
		action, revision, tag := match[1], match[2], match[3]
		expectedPin, ok := expected[action]
		if !ok {
			t.Errorf("unexpected action %q", action)
			continue
		}
		if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(revision) {
			t.Errorf("%s must be pinned to a full 40-character commit SHA, got %q", action, revision)
		}
		if got := revision + " # " + tag; got != expectedPin {
			t.Errorf("%s must map to audited pin %q, got %q", action, expectedPin, got)
		}
		delete(expected, action)
	}
	for action := range expected {
		t.Errorf("Pages workflow is missing required action %q", action)
	}
}

func TestPagesWorkflowPreservesDeploymentContract(t *testing.T) {
	content, err := os.ReadFile(".github/workflows/pages.yml")
	if err != nil {
		t.Fatalf("read Pages workflow: %v", err)
	}
	workflow := string(content)
	for _, required := range []string{
		"branches:\n      - main",
		"workflow_dispatch:",
		"contents: read",
		"pages: write",
		"id-token: write",
		"path: ./site",
		"id: deployment",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("Pages workflow must retain %q", required)
		}
	}
}
