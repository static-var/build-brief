package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadUsesExplicitConfigPath(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "custom.json")
	writeConfig(t, configPath, `{
		"matches": [
			{"name": "Firebase Test Lab", "pattern": "https://console\\.firebase\\.google\\.com/[^\\s]+"}
		]
	}`)

	cfg, loadedPath, err := Load(projectDir, configPath)
	if err != nil {
		t.Fatalf("load explicit config: %v", err)
	}

	if loadedPath != configPath {
		t.Fatalf("expected loaded path %q, got %q", configPath, loadedPath)
	}
	if len(cfg.Matches) != 1 {
		t.Fatalf("expected one match, got %+v", cfg.Matches)
	}
	if cfg.Matches[0].Name != "Firebase Test Lab" {
		t.Fatalf("unexpected match name %q", cfg.Matches[0].Name)
	}
}

func TestLoadResolvesRelativeExplicitPathAgainstProjectDir(t *testing.T) {
	callerDir := t.TempDir()
	projectDir := t.TempDir()
	configPath := filepath.Join(projectDir, "custom.json")
	writeConfig(t, configPath, `{
		"matches": [
			{"name": "emulator.wtf", "pattern": "https://app\\.emulator\\.wtf/[^\\s]+"}
		]
	}`)

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get caller directory: %v", err)
	}
	if err := os.Chdir(callerDir); err != nil {
		t.Fatalf("change caller directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Errorf("restore caller directory: %v", err)
		}
	})

	cfg, loadedPath, err := Load(projectDir, "custom.json")
	if err != nil {
		t.Fatalf("load relative explicit config: %v", err)
	}

	if loadedPath != configPath {
		t.Fatalf("expected loaded path %q, got %q", configPath, loadedPath)
	}
	if len(cfg.Matches) != 1 || cfg.Matches[0].Name != "emulator.wtf" {
		t.Fatalf("unexpected matches: %+v", cfg.Matches)
	}
}

func TestLoadUsesDefaultProjectConfig(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(projectDir, ".build-brief.json")
	writeConfig(t, configPath, `{
		"matches": [
			{"name": "emulator.wtf", "pattern": "https://app\\.emulator\\.wtf/[^\\s]+"}
		]
	}`)

	cfg, loadedPath, err := Load(projectDir, "")
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}

	if loadedPath != configPath {
		t.Fatalf("expected loaded path %q, got %q", configPath, loadedPath)
	}
	if len(cfg.Matches) != 1 || cfg.Matches[0].Name != "emulator.wtf" {
		t.Fatalf("unexpected matches: %+v", cfg.Matches)
	}
}

func TestLoadAllowsMissingOptionalDefaultConfig(t *testing.T) {
	cfg, loadedPath, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatalf("load missing optional default config: %v", err)
	}

	if loadedPath != "" {
		t.Fatalf("expected no loaded path, got %q", loadedPath)
	}
	if len(cfg.Matches) != 0 {
		t.Fatalf("expected no matches, got %+v", cfg.Matches)
	}
}

func TestLoadAllowsRulesBeyondRuntimeCap(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "custom.json")
	matches := strings.TrimSuffix(strings.Repeat(`{"name":"rule","pattern":"match"},`, CustomMatchRuleLimit+1), ",")
	writeConfig(t, configPath, `{"matches":[`+matches+`]}`)

	cfg, _, err := Load(t.TempDir(), configPath)
	if err != nil {
		t.Fatalf("load permissive config: %v", err)
	}
	if len(cfg.Matches) != CustomMatchRuleLimit+1 {
		t.Fatalf("matches = %d, want %d", len(cfg.Matches), CustomMatchRuleLimit+1)
	}
}

func TestCustomMatchRuntimeLimits(t *testing.T) {
	if CustomMatchRuleLimit != 64 {
		t.Fatalf("rule limit = %d, want 64", CustomMatchRuleLimit)
	}
	if CustomMatchUniqueResultLimitPerRule != 8 {
		t.Fatalf("unique-result limit = %d, want 8", CustomMatchUniqueResultLimitPerRule)
	}
}

func TestCustomMatchLimitDocumentationMatchesExportedLimits(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate config test file")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	for _, path := range []string{filepath.Join(root, "README.md"), filepath.Join(root, "site", "index.html")} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, expected := range []string{
			fmt.Sprintf("%d rules", CustomMatchRuleLimit),
			fmt.Sprintf("%d unique matches", CustomMatchUniqueResultLimitPerRule),
		} {
			if !strings.Contains(string(content), expected) {
				t.Fatalf("%s does not document %q", path, expected)
			}
		}
	}
}

func TestGainsDocumentationStatesLocalRetentionBehavior(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate config test file")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	for _, path := range []string{filepath.Join(root, "README.md"), filepath.Join(root, "site", "index.html")} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, expected := range []string{
			"no gains data is transmitted",
			"Entries older than 90 days are pruned when the next tracked run is recorded",
			"inactive history may remain until then",
			"build-brief gains --reset",
		} {
			if !strings.Contains(string(content), expected) {
				t.Fatalf("%s does not document %q", path, expected)
			}
		}
	}
}

func TestLoadRejectsInvalidRegex(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "custom.json")
	writeConfig(t, configPath, `{
		"matches": [
			{"name": "Broken", "pattern": "["}
		]
	}`)

	_, _, err := Load(t.TempDir(), configPath)
	if err == nil {
		t.Fatal("expected invalid regex error")
	}
	if !strings.Contains(err.Error(), "match \"Broken\" has invalid regex") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsUnnamedMatch(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "custom.json")
	writeConfig(t, configPath, `{
		"matches": [
			{"pattern": "https://example\\.com/[^\\s]+"}
		]
	}`)

	_, _, err := Load(t.TempDir(), configPath)
	if err == nil {
		t.Fatal("expected missing name error")
	}
	if !strings.Contains(err.Error(), "matches[0].name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
