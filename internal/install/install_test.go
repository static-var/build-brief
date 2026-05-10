package install

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallLocalRequiresExistingAgentsUnlessForced(t *testing.T) {
	original := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = original
	})

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

	if err := upsertInstructionBlock(path, localInstructions(false), true); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := upsertInstructionBlock(path, localInstructions(false), true); err != nil {
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
	original := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = original
	})

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

func TestInstallGlobalCreatesPiExtensionWithoutAgentsFile(t *testing.T) {
	original := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = original
	})

	home := t.TempDir()
	target := filepath.Join(home, ".pi", "agent", "AGENTS.md")

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				ID:             "pi-coding-agent",
				Name:           "Pi Coding Agent",
				GlobalTargets:  []string{target},
				SupportsPlugin: true,
			},
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}

	extensionTarget := filepath.Join(home, ".pi", "agent", "extensions", "build-brief", "index.ts")
	if !containsPath(installed, "Pi Coding Agent plugin -> "+extensionTarget) {
		t.Fatalf("expected extension install entry, got %v", installed)
	}

	content, err := os.ReadFile(extensionTarget)
	if err != nil {
		t.Fatalf("read extension: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "build-brief") || !strings.Contains(text, "tool_call") || !strings.Contains(text, "event.input.command = rewritten") {
		t.Fatalf("expected Pi extension to rewrite bash commands via build-brief, got %q", text)
	}
	if strings.Contains(text, "RTK") {
		t.Fatalf("expected extension source to stay RTK-free, got %q", text)
	}
}

func TestInstallGlobalCreatesOpenCodePluginWithoutAgentsFile(t *testing.T) {
	original := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = original
	})

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

func TestInstallGlobalCreatesCopilotPluginWithoutInstructionsFile(t *testing.T) {
	originalRTK := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = originalRTK
	})

	var installedBinary string
	var installedPluginDir string
	originalInstall := runCopilotPluginInstall
	runCopilotPluginInstall = func(binary, pluginDir string) error {
		installedBinary = binary
		installedPluginDir = pluginDir
		return nil
	}
	t.Cleanup(func() {
		runCopilotPluginInstall = originalInstall
	})

	home := t.TempDir()
	setFakeHome(t, home)

	target := filepath.Join(home, ".copilot", "copilot-instructions.md")

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				ID:             "copilot-cli",
				Name:           "GitHub Copilot CLI",
				GlobalTargets:  []string{target},
				SupportsPlugin: true,
			},
			DetectedBinary:  "/usr/local/bin/copilot",
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}

	pluginRoot := filepath.Join(home, ".copilot", "plugins", "build-brief")
	if !containsPath(installed, "GitHub Copilot CLI plugin -> "+pluginRoot) {
		t.Fatalf("expected copilot plugin install entry, got %v", installed)
	}
	if installedBinary != "/usr/local/bin/copilot" {
		t.Fatalf("expected detected copilot binary to be used, got %q", installedBinary)
	}
	if installedPluginDir != pluginRoot {
		t.Fatalf("expected plugin install dir %q, got %q", pluginRoot, installedPluginDir)
	}

	manifestPath := filepath.Join(pluginRoot, "plugin.json")
	manifestContent, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read copilot plugin manifest: %v", err)
	}
	if !strings.Contains(string(manifestContent), `"hooks": "hooks.json"`) {
		t.Fatalf("expected hooks manifest entry, got %q", string(manifestContent))
	}

	hooksPath := filepath.Join(pluginRoot, "hooks.json")
	hooksContent, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read copilot hooks: %v", err)
	}
	if !strings.Contains(string(hooksContent), `"preToolUse"`) {
		t.Fatalf("expected preToolUse hook, got %q", string(hooksContent))
	}
	if !strings.Contains(string(hooksContent), "permissionDecision") {
		t.Fatalf("expected deny decision logic, got %q", string(hooksContent))
	}
	if !strings.Contains(string(hooksContent), "['build-brief', 'rewrite', command]") {
		t.Fatalf("expected rewrite delegation, got %q", string(hooksContent))
	}
}

func TestInstallGlobalCreatesClaudePluginWithoutInstructionsFile(t *testing.T) {
	originalRTK := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = originalRTK
	})

	var installedBinary string
	var installedMarketplaceDir string
	var installedPluginRef string
	originalInstall := runClaudePluginInstall
	runClaudePluginInstall = func(binary, marketplaceDir, pluginRef string) error {
		installedBinary = binary
		installedMarketplaceDir = marketplaceDir
		installedPluginRef = pluginRef
		return nil
	}
	t.Cleanup(func() {
		runClaudePluginInstall = originalInstall
	})

	home := t.TempDir()
	setFakeHome(t, home)

	target := filepath.Join(home, ".claude", "CLAUDE.md")

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				ID:             "claude-code",
				Name:           "Claude Code",
				GlobalTargets:  []string{target},
				SupportsPlugin: true,
			},
			DetectedBinary:  "/usr/local/bin/claude",
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}

	pluginRoot := filepath.Join(home, ".claude", "marketplaces", "build-brief", "plugins", "build-brief")
	if !containsPath(installed, "Claude Code plugin -> "+pluginRoot) {
		t.Fatalf("expected claude plugin install entry, got %v", installed)
	}
	if installedBinary != "/usr/local/bin/claude" {
		t.Fatalf("expected detected claude binary to be used, got %q", installedBinary)
	}
	if installedMarketplaceDir != filepath.Join(home, ".claude", "marketplaces", "build-brief") {
		t.Fatalf("unexpected marketplace dir %q", installedMarketplaceDir)
	}
	if installedPluginRef != "build-brief@build-brief-local" {
		t.Fatalf("unexpected install target %q", installedPluginRef)
	}

	manifestPath := filepath.Join(pluginRoot, ".claude-plugin", "plugin.json")
	manifestContent, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read claude plugin manifest: %v", err)
	}
	if !strings.Contains(string(manifestContent), `"name": "build-brief"`) {
		t.Fatalf("expected plugin manifest name, got %q", string(manifestContent))
	}

	marketplacePath := filepath.Join(home, ".claude", "marketplaces", "build-brief", ".claude-plugin", "marketplace.json")
	marketplaceContent, err := os.ReadFile(marketplacePath)
	if err != nil {
		t.Fatalf("read claude marketplace: %v", err)
	}
	if !strings.Contains(string(marketplaceContent), `"name": "build-brief-local"`) {
		t.Fatalf("expected marketplace name, got %q", string(marketplaceContent))
	}

	hooksPath := filepath.Join(pluginRoot, "hooks", "hooks.json")
	hooksContent, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read claude hooks: %v", err)
	}
	if !strings.Contains(string(hooksContent), `"PreToolUse"`) {
		t.Fatalf("expected PreToolUse hook, got %q", string(hooksContent))
	}
	if !strings.Contains(string(hooksContent), `${CLAUDE_PLUGIN_ROOT}/scripts/pretooluse-build-brief.sh`) {
		t.Fatalf("expected plugin root hook command, got %q", string(hooksContent))
	}

	scriptPath := filepath.Join(pluginRoot, "scripts", "pretooluse-build-brief.sh")
	scriptContent, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read claude pretooluse script: %v", err)
	}
	if !strings.Contains(string(scriptContent), `"hookSpecificOutput"`) {
		t.Fatalf("expected deny decision output, got %q", string(scriptContent))
	}
	if !strings.Contains(string(scriptContent), "build-brief rewrite") {
		t.Fatalf("expected rewrite delegation, got %q", string(scriptContent))
	}
}

func TestInstallGlobalCreatesCodexPluginWithoutAgentsFile(t *testing.T) {
	originalRTK := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = originalRTK
	})

	home := t.TempDir()
	setFakeHome(t, home)

	target := filepath.Join(home, ".codex", "AGENTS.md")
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create codex config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[features]\ncodex_hooks = true\n\n[model]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatalf("write existing codex config: %v", err)
	}

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				ID:             "codex-cli",
				Name:           "Codex App & CLI",
				GlobalTargets:  []string{target},
				SupportsPlugin: true,
			},
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}

	pluginRoot := filepath.Join(home, ".codex", "plugins", "build-brief")
	if !containsPath(installed, "Codex App & CLI plugin -> "+pluginRoot) {
		t.Fatalf("expected codex plugin install entry, got %v", installed)
	}

	manifestPath := filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
	manifestContent, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read codex plugin manifest: %v", err)
	}
	if !strings.Contains(string(manifestContent), `"hooks": "./hooks.json"`) {
		t.Fatalf("expected hook manifest entry, got %q", string(manifestContent))
	}

	hooksPath := filepath.Join(pluginRoot, "hooks.json")
	hooksContent, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read codex hooks: %v", err)
	}
	if !strings.Contains(string(hooksContent), "['build-brief', 'rewrite', command]") {
		t.Fatalf("expected rewrite delegation in hooks, got %q", string(hooksContent))
	}
	if !strings.Contains(string(hooksContent), "PreToolUse") {
		t.Fatalf("expected PreToolUse hook in hooks.json, got %q", string(hooksContent))
	}

	marketplacePath := filepath.Join(home, ".agents", "plugins", "marketplace.json")
	marketplaceContent, err := os.ReadFile(marketplacePath)
	if err != nil {
		t.Fatalf("read marketplace: %v", err)
	}
	var marketplace map[string]any
	if err := json.Unmarshal(marketplaceContent, &marketplace); err != nil {
		t.Fatalf("unmarshal marketplace: %v", err)
	}
	plugins, ok := marketplace["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("expected one marketplace plugin entry, got %v", marketplace["plugins"])
	}
	plugin, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("expected marketplace plugin object, got %T", plugins[0])
	}
	source, ok := plugin["source"].(map[string]any)
	if !ok {
		t.Fatalf("expected marketplace source object, got %T", plugin["source"])
	}
	if source["path"] != "./.codex/plugins/build-brief" {
		t.Fatalf("expected marketplace source path %q, got %v", "./.codex/plugins/build-brief", source["path"])
	}

	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	configText := string(configContent)
	if !strings.Contains(configText, "[features]") || !strings.Contains(configText, "hooks = true") {
		t.Fatalf("expected hooks feature flag, got %q", configText)
	}
	if !strings.Contains(configText, "plugin_hooks = true") {
		t.Fatalf("expected plugin_hooks feature flag, got %q", configText)
	}
	if strings.Contains(configText, "codex_hooks") {
		t.Fatalf("expected deprecated codex_hooks flag to be removed, got %q", configText)
	}
	if strings.Contains(configText, "suppress_unstable_features_warning") {
		t.Fatalf("expected installer not to suppress unstable feature warnings, got %q", configText)
	}
	if !strings.Contains(configText, `[hooks.state."build-brief@local-user-plugins:hooks.json:pre_tool_use:0:0"]`) {
		t.Fatalf("expected trusted hook state entry, got %q", configText)
	}
	if !strings.Contains(configText, "trusted_hash = \"sha256:") {
		t.Fatalf("expected trusted hook hash, got %q", configText)
	}
	if !strings.Contains(configText, `[plugins."build-brief@local-user-plugins"]`) || !strings.Contains(configText, "enabled = true") {
		t.Fatalf("expected enabled plugin entry, got %q", configText)
	}

	cacheManifestPath := filepath.Join(home, ".codex", "plugins", "cache", "local-user-plugins", "build-brief", "local", ".codex-plugin", "plugin.json")
	if _, err := os.Stat(cacheManifestPath); err != nil {
		t.Fatalf("expected cached codex plugin manifest, stat err=%v", err)
	}
}

func TestInstallGlobalUpdatesExistingFiles(t *testing.T) {
	original := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = original
	})

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
	original := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = original
	})

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

func TestInstallGlobalUpdatesClaudeInstructionsAndPlugin(t *testing.T) {
	originalRTK := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = originalRTK
	})

	originalInstall := runClaudePluginInstall
	runClaudePluginInstall = func(binary, marketplaceDir, pluginRef string) error { return nil }
	t.Cleanup(func() {
		runClaudePluginInstall = originalInstall
	})

	home := t.TempDir()
	setFakeHome(t, home)

	target := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("Existing content\n"), 0o644); err != nil {
		t.Fatalf("create CLAUDE.md: %v", err)
	}

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				ID:                 "claude-code",
				Name:               "Claude Code",
				GlobalTargets:      []string{target},
				SupportsHookGuides: true,
				SupportsPlugin:     true,
			},
			DetectedBinary:  "/usr/local/bin/claude",
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
	if len(installed) != 2 {
		t.Fatalf("expected plugin + CLAUDE install entries, got %v", installed)
	}

	claudeContent, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	text := string(claudeContent)
	if !strings.Contains(text, "managed Claude Code plugin") {
		t.Fatalf("expected Claude plugin guidance in CLAUDE.md, got %q", text)
	}
	if !strings.Contains(text, "PreToolUse") {
		t.Fatalf("expected PreToolUse guidance in CLAUDE.md, got %q", text)
	}
}

func TestInstallGlobalUpdatesCopilotInstructionsAndPlugin(t *testing.T) {
	originalRTK := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = originalRTK
	})

	originalInstall := runCopilotPluginInstall
	runCopilotPluginInstall = func(binary, pluginDir string) error { return nil }
	t.Cleanup(func() {
		runCopilotPluginInstall = originalInstall
	})

	home := t.TempDir()
	setFakeHome(t, home)

	target := filepath.Join(home, ".copilot", "copilot-instructions.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("Existing content\n"), 0o644); err != nil {
		t.Fatalf("create instructions: %v", err)
	}

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				ID:             "copilot-cli",
				Name:           "GitHub Copilot CLI",
				GlobalTargets:  []string{target},
				SupportsPlugin: true,
			},
			DetectedBinary:  "/usr/local/bin/copilot",
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
	if len(installed) != 2 {
		t.Fatalf("expected plugin + instructions install entries, got %v", installed)
	}

	instructionsContent, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read copilot instructions: %v", err)
	}
	text := string(instructionsContent)
	if !strings.Contains(text, "managed GitHub Copilot CLI plugin") {
		t.Fatalf("expected copilot plugin guidance in instructions, got %q", text)
	}
	if !strings.Contains(text, "preToolUse") {
		t.Fatalf("expected hook guidance mention in instructions, got %q", text)
	}
}

func TestInstallGlobalUpdatesCodexAgentsAndPlugin(t *testing.T) {
	originalRTK := runRTKHelp
	runRTKHelp = func() error { return errors.New("missing") }
	t.Cleanup(func() {
		runRTKHelp = originalRTK
	})

	home := t.TempDir()
	setFakeHome(t, home)

	target := filepath.Join(home, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("Existing content\n"), 0o644); err != nil {
		t.Fatalf("create AGENTS: %v", err)
	}

	installed, failures := InstallGlobal([]DetectedTool{
		{
			Tool: Tool{
				ID:             "codex-cli",
				Name:           "Codex App & CLI",
				GlobalTargets:  []string{target},
				SupportsPlugin: true,
			},
			PreferredTarget: target,
		},
	})

	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
	if len(installed) != 2 {
		t.Fatalf("expected plugin + AGENTS install entries, got %v", installed)
	}

	agentsContent, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read AGENTS: %v", err)
	}
	agentsText := string(agentsContent)
	if !strings.Contains(agentsText, "managed Codex App & CLI plugin") {
		t.Fatalf("expected Codex App & CLI plugin guidance in AGENTS.md, got %q", agentsText)
	}
	if !strings.Contains(agentsText, "PreToolUse") {
		t.Fatalf("expected PreToolUse guidance in AGENTS.md, got %q", agentsText)
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

func TestLocalInstructionsAddRTKGuidanceWhenDetected(t *testing.T) {
	text := localInstructions(true)
	for _, expected := range []string{
		"build-brief gradle test && build-brief gradle check",
		"including chained `&&`, `||`, and `;` segments",
		"RTK is installed on this machine.",
		"Prefer `build-brief` directly for Gradle commands",
		"rather than sending Gradle through RTK first",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected local instructions to contain %q, got %q", expected, text)
		}
	}
}

func TestGlobalInstructionsAddRTKGuidanceWhenDetected(t *testing.T) {
	text := globalInstructions(Tool{Name: "GitHub Copilot CLI"}, true)
	for _, expected := range []string{
		"build-brief gradle test && build-brief gradle check",
		"route chained `&&`, `||`, and `;` Gradle segments to `build-brief` too",
		"RTK is installed on this machine.",
		"Prefer `build-brief` directly for Gradle commands",
		"instead of sending Gradle through RTK first",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected global instructions to contain %q, got %q", expected, text)
		}
	}
}

func TestGlobalInstructionsAddChainAwareHookAndPluginGuidance(t *testing.T) {
	text := globalInstructions(Tool{ID: "copilot-cli", SupportsHookGuides: true, SupportsPlugin: true}, false)
	for _, expected := range []string{
		"including chained `&&`, `||`, and `;` shell segments",
		"including chained `&&`, `||`, and `;` Gradle segments",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected global instructions to contain %q, got %q", expected, text)
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

func TestSelectionMenuHandleKeys(t *testing.T) {
	tools := []DetectedTool{
		{Tool: Tool{Name: "Tool 1"}},
		{Tool: Tool{Name: "Tool 2"}},
		{Tool: Tool{Name: "Tool 3"}},
	}

	menu := newSelectionMenu(tools)

	if done, cancel := menu.handleKey(selectionKeyToggle); done || cancel {
		t.Fatalf("toggle should not finish or cancel")
	}
	if !menu.selected[0] {
		t.Fatalf("expected first item to toggle on")
	}

	menu.handleKey(selectionKeyDown)
	menu.handleKey(selectionKeyDown)
	if menu.cursor != 2 {
		t.Fatalf("expected cursor at 2, got %d", menu.cursor)
	}

	menu.handleKey(selectionKeyDown)
	if menu.cursor != 0 {
		t.Fatalf("expected cursor to wrap to 0, got %d", menu.cursor)
	}

	menu.handleKey(selectionKeyUp)
	if menu.cursor != 2 {
		t.Fatalf("expected cursor to wrap to last item, got %d", menu.cursor)
	}

	menu.handleKey(selectionKeyToggle)
	selected := menu.selectedTools()
	if len(selected) != 2 {
		t.Fatalf("expected two selected tools, got %d", len(selected))
	}

	if done, cancel := menu.handleKey(selectionKeySubmit); !done || cancel {
		t.Fatalf("submit should finish without cancelling")
	}
	if done, cancel := menu.handleKey(selectionKeyCancel); done || !cancel {
		t.Fatalf("cancel should cancel without finishing")
	}
}

func TestSelectionMenuRenderUsesRawSafeLineEndings(t *testing.T) {
	menu := newSelectionMenu([]DetectedTool{
		{Tool: Tool{Name: "Tool 1"}, PreferredTarget: "/tmp/tool-1"},
	})
	menu.selected[0] = true

	var out bytes.Buffer
	if err := menu.render(&out); err != nil {
		t.Fatalf("render: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"Detected AI tools and global instruction targets:\r\n",
		"> [x] Tool 1\r\n",
		"    target: /tmp/tool-1\r\n",
		"Selected: 1\r\n",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected rendered output to contain %q, got %q", expected, rendered)
		}
	}
}

func TestReadSelectionKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected selectionKey
	}{
		{name: "up arrow", input: "\x1b[A", expected: selectionKeyUp},
		{name: "down arrow", input: "\x1b[B", expected: selectionKeyDown},
		{name: "application cursor up arrow", input: "\x1bOA", expected: selectionKeyUp},
		{name: "application cursor down arrow", input: "\x1bOB", expected: selectionKeyDown},
		{name: "modified up arrow", input: "\x1b[1;5A", expected: selectionKeyUp},
		{name: "modified down arrow", input: "\x1b[1;5B", expected: selectionKeyDown},
		{name: "vim up", input: "k", expected: selectionKeyUp},
		{name: "vim down", input: "j", expected: selectionKeyDown},
		{name: "toggle", input: " ", expected: selectionKeyToggle},
		{name: "submit", input: "\n", expected: selectionKeySubmit},
		{name: "cancel", input: "q", expected: selectionKeyCancel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := readSelectionKey(bufio.NewReader(strings.NewReader(tt.input)))
			if err != nil {
				t.Fatalf("readSelectionKey: %v", err)
			}
			if key != tt.expected {
				t.Fatalf("expected %v, got %v", tt.expected, key)
			}
		})
	}
}

func TestDetectGlobalToolsDetectsCodexAppDirectory(t *testing.T) {
	home := t.TempDir()
	setFakeHome(t, home)

	appDir := filepath.Join(home, "Library", "Application Support", "Codex")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("create app dir: %v", err)
	}

	tools, err := DetectGlobalTools()
	if err != nil {
		t.Fatalf("DetectGlobalTools: %v", err)
	}

	var codex *DetectedTool
	for i := range tools {
		if tools[i].Tool.ID == "codex-cli" {
			codex = &tools[i]
			break
		}
	}
	if codex == nil {
		t.Fatalf("expected Codex App & CLI to be detected from app directory, got %v", tools)
	}
	if codex.Tool.Name != "Codex App & CLI" {
		t.Fatalf("unexpected codex name %q", codex.Tool.Name)
	}
	if codex.PreferredTarget != filepath.Join(home, ".codex", "AGENTS.md") {
		t.Fatalf("expected shared ~/.codex AGENTS target, got %q", codex.PreferredTarget)
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

	codex := findTool(t, tools, "codex-cli")
	if codex.Name != "Codex App & CLI" {
		t.Fatalf("expected Codex App & CLI name, got %q", codex.Name)
	}
	if !codex.SupportsPlugin {
		t.Fatalf("expected codex to support plugins, got %+v", codex)
	}
	if !containsPath(codex.DetectionPaths, filepath.Join(home, ".codex")) {
		t.Fatalf("expected ~/.codex detection path in %v", codex.DetectionPaths)
	}
	if !containsPath(codex.DetectionPaths, filepath.Join(home, "Library", "Application Support", "Codex")) {
		t.Fatalf("expected Codex app detection path in %v", codex.DetectionPaths)
	}

	copilot := findTool(t, tools, "copilot-cli")
	if !copilot.SupportsPlugin {
		t.Fatalf("expected copilot to support plugins, got %+v", copilot)
	}

	claude := findTool(t, tools, "claude-code")
	if !claude.SupportsPlugin {
		t.Fatalf("expected claude to support plugins, got %+v", claude)
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

func setFakeHome(t *testing.T, home string) {
	t.Helper()

	originalHome := userHomeDir
	originalConfig := userConfigDir
	userHomeDir = func() (string, error) { return home, nil }
	userConfigDir = func() (string, error) { return filepath.Join(home, ".config"), nil }
	t.Cleanup(func() {
		userHomeDir = originalHome
		userConfigDir = originalConfig
	})
}
