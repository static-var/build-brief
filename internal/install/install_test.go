package install

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallLocalRequiresExistingAgentsUnlessForced(t *testing.T) {
	dir := t.TempDir()

	_, err := InstallLocal(dir, false)
	if err == nil || !MissingAgentsError(err) {
		t.Fatalf("expected missing AGENTS error, got %v", err)
	}

	target, err := InstallLocal(dir, true)
	if err != nil {
		t.Fatalf("force install local: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}

	text := string(content)
	if !strings.Contains(text, "Prefer `build-brief gradle ...` for PATH Gradle") {
		t.Fatalf("unexpected local instructions: %q", text)
	}
	if strings.Contains(text, "RTK") {
		t.Fatalf("expected local instructions to stay RTK-free, got %q", text)
	}
}

func TestUpsertInstructionBlockIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")

	if err := upsertInstructionBlock(path, localInstructions(), true); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := upsertInstructionBlock(path, localInstructions(), true); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read instruction file: %v", err)
	}

	text := string(content)
	if count := strings.Count(text, blockStart); count != 1 {
		t.Fatalf("expected one instruction block, got %d in %q", count, text)
	}
}

func TestInstallGlobalDoesNotCreateMissingFiles(t *testing.T) {
	target := filepath.Join(t.TempDir(), "copilot-instructions.md")

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				Name:          "GitHub Copilot CLI",
				GlobalTargets: []string{target},
			},
			PreferredTarget: target,
		},
	})

	if len(installed) != 0 {
		t.Fatalf("expected no successful installs, got %v", installed)
	}

	if len(failures) != 1 {
		t.Fatalf("expected one failure, got %d", len(failures))
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected missing file to stay missing, stat err=%v", err)
	}
}

func TestInstallGlobalCreatesOpenCodePluginWithoutAgentsFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "opencode", "AGENTS.md")

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				ID:             "opencode",
				Name:           "OpenCode",
				GlobalTargets:  []string{target},
				SupportsPlugin: true,
			},
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}

	pluginTarget := filepath.Join(dir, "opencode", "plugins", "build-brief.ts")
	if !containsPath(installed, "OpenCode plugin -> "+pluginTarget) {
		t.Fatalf("expected plugin install entry, got %v", installed)
	}

	content, err := os.ReadFile(pluginTarget)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	if !strings.Contains(string(content), "build-brief rewrite") {
		t.Fatalf("expected rewrite delegation in plugin, got %q", string(content))
	}
	if strings.Contains(string(content), "RTK") {
		t.Fatalf("expected plugin source to stay RTK-free, got %q", string(content))
	}
}

func TestInstallGlobalUpdatesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "copilot-instructions.md")

	if err := os.WriteFile(target, []byte("Existing content\n"), 0o644); err != nil {
		t.Fatalf("create existing file: %v", err)
	}

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				Name:          "GitHub Copilot CLI",
				GlobalTargets: []string{target},
			},
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}

	if len(installed) != 1 {
		t.Fatalf("expected one install, got %d", len(installed))
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if !strings.Contains(string(content), "Existing content") {
		t.Error("expected existing content to be preserved")
	}
	if !strings.Contains(string(content), "build-brief") {
		t.Error("expected build-brief instructions to be added")
	}
}

func TestInstallGlobalUpdatesOpenCodeAgentsAndPlugin(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "opencode", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("Existing content\n"), 0o644); err != nil {
		t.Fatalf("create AGENTS: %v", err)
	}

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				ID:                 "opencode",
				Name:               "OpenCode",
				GlobalTargets:      []string{target},
				SupportsHookGuides: true,
				SupportsPlugin:     true,
			},
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
	if len(installed) != 2 {
		t.Fatalf("expected two install entries, got %v", installed)
	}

	agentsContent, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read AGENTS: %v", err)
	}
	if !strings.Contains(string(agentsContent), "Plugin guidance") {
		t.Fatalf("expected plugin guidance in AGENTS.md, got %q", string(agentsContent))
	}
	if strings.Contains(string(agentsContent), "RTK") {
		t.Fatalf("expected managed AGENTS.md block to stay RTK-free, got %q", string(agentsContent))
	}

	pluginTarget := filepath.Join(dir, "opencode", "plugins", "build-brief.ts")
	if _, err := os.Stat(pluginTarget); err != nil {
		t.Fatalf("expected plugin to exist, stat err=%v", err)
	}
}

func TestRTKInstalled(t *testing.T) {
	original := runRTKHelp
	t.Cleanup(func() {
		runRTKHelp = original
	})

	runRTKHelp = func() error { return nil }
	if !RTKInstalled() {
		t.Fatal("expected RTKInstalled to report true when rtk --help succeeds")
	}

	runRTKHelp = func() error { return errors.New("missing") }
	if RTKInstalled() {
		t.Fatal("expected RTKInstalled to report false when rtk --help fails")
	}
}

func TestRTKInstallNotice(t *testing.T) {
	notice := RTKInstallNotice()
	for _, expected := range []string{
		"RTK detected on this machine.",
		"Prefer build-brief over RTK, raw gradle, and ./gradlew for Gradle commands.",
		"Let raw gradle/./gradlew commands be rewritten by build-brief hooks or plugins",
	} {
		if !strings.Contains(notice, expected) {
			t.Fatalf("expected RTK install notice to contain %q, got %q", expected, notice)
		}
	}
}

func TestPromptForSelection(t *testing.T) {
	tools := []DetectedTool{
		{Tool: Tool{ID: "tool1", Name: "Tool 1"}},
		{Tool: Tool{ID: "tool2", Name: "Tool 2"}},
		{Tool: Tool{ID: "tool3", Name: "Tool 3"}},
	}

	tests := []struct {
		input    string
		expected int
		err      bool
	}{
		{"1\n", 1, false},
		{"1,3\n", 2, false},
		{"*\n", 3, false},
		{"all\n", 3, false},
		{"\n", 0, false},
		{"99\n", 0, true},
		{"0\n", 0, true},
		{"foo\n", 0, true},
	}

	for _, tt := range tests {
		var out bytes.Buffer
		selected, err := PromptForSelection(strings.NewReader(tt.input), &out, tools)
		if tt.err {
			if err == nil {
				t.Errorf("input %q: expected error", tt.input)
			}
			continue
		}

		if err != nil {
			t.Errorf("input %q: unexpected error: %v", tt.input, err)
			continue
		}

		if len(selected) != tt.expected {
			t.Errorf("input %q: expected %d selected, got %d", tt.input, tt.expected, len(selected))
		}
	}
}

func TestKnownToolsUseOpenCodeConfigTargets(t *testing.T) {
	tools, err := knownTools()
	if err != nil {
		t.Fatalf("knownTools: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	opencode := findTool(t, tools, "opencode")
	if !containsPath(opencode.GlobalTargets, filepath.Join(home, ".config", "opencode", "AGENTS.md")) {
		t.Fatalf("expected ~/.config target in %v", opencode.GlobalTargets)
	}
	if !containsPath(opencode.GlobalTargets, filepath.Join(home, ".opencode", "AGENTS.md")) {
		t.Fatalf("expected ~/.opencode target in %v", opencode.GlobalTargets)
	}
}

func findTool(t *testing.T, tools []Tool, id string) Tool {
	t.Helper()

	for _, tool := range tools {
		if tool.ID == id {
			return tool
		}
	}

	t.Fatalf("tool %q not found", id)
	return Tool{}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}

	return false
}
