package install

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestTOMLEditsPreserveCommentedSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "# preferences\n[features] # flags\n\"hooks\"\t= false # enable me\ncodex_hooks = true\n[model] # unrelated\nname = \"test\"\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := editTOMLFile(path, tomlEdit{section: "[features]", key: "hooks", value: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := editTOMLFile(path, tomlEdit{section: "[features]", key: "codex_hooks", remove: true}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Count(text, "[features]") != 1 || strings.Contains(text, "codex_hooks") || !strings.Contains(text, "true # enable me") || !strings.Contains(text, "[model] # unrelated\nname = \"test\"") {
		t.Fatalf("configuration was not safely updated:\n%s", text)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("permissions changed: %v", info.Mode())
	}
}

func TestTOMLEditsRejectInvalidInputWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "[features]\nhooks = false\n[features]\nhooks = true\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := editTOMLFile(path, tomlEdit{section: "[features]", key: "hooks", value: "true"}); err == nil {
		t.Fatal("accepted duplicate TOML tables")
	}
	content, _ := os.ReadFile(path)
	if string(content) != original {
		t.Fatalf("invalid input was changed: %s", content)
	}
}

func TestGuardrailsHandleMixedWrappedAndRawChains(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash hooks")
	}
	for _, program := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skip(program + " unavailable")
		}
	}
	dir := t.TempDir()
	rewritten := "build-brief ./gradlew test && build-brief ./gradlew check"
	if err := os.WriteFile(filepath.Join(dir, "build-brief"), []byte("#!/bin/sh\nprintf '%s\\n' '"+rewritten+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := "build-brief ./gradlew test && ./gradlew check"
	arguments, _ := json.Marshal(map[string]string{"command": command})
	cases := []struct {
		name, script string
		payload      any
	}{
		{"codex", codexPreToolUseCommand(), map[string]any{"tool_input": map[string]string{"command": command}}},
		{"claude", claudePluginPreToolUseScript(), map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": command}}},
		{"copilot", copilotPreToolUseCommand(), map[string]any{"toolName": "bash", "toolArgs": string(arguments)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, _ := json.Marshal(tc.payload)
			cmd := exec.Command("bash", "-c", tc.script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			cmd.Stdin = strings.NewReader(string(payload))
			output, err := cmd.CombinedOutput()
			if tc.name == "codex" {
				if code, ok := err.(*exec.ExitError); !ok || code.ExitCode() != 2 {
					t.Fatalf("expected blocking exit 2, got %v: %s", err, output)
				}
			} else if err != nil {
				t.Fatalf("hook: %v: %s", err, output)
			}
			if !strings.Contains(string(output), rewritten) {
				t.Fatalf("missing mixed-chain correction: %s", output)
			}
		})
	}
}

func TestTOMLEditsKeepMultilineStringsAndOtherTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "notes = '''\n[features]\nhooks = false\n'''\n[ features ] # flags\nplugin_hooks = false\n[other] # leave intact\nhooks = false\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := editTOMLFile(path, tomlEdit{section: "[features]", key: "hooks", value: "true"}); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "notes = '''\n[features]\nhooks = false\n'''") || !strings.Contains(string(content), "hooks = true\n[other] # leave intact\nhooks = false\n") {
		t.Fatalf("changed unrelated TOML: %s", content)
	}
}

func TestTOMLEditsApplyAllChangesOrNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "[features]\nhooks = false\nplugin_hooks = {enabled = false}\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	err := editTOMLFile(path, tomlEdit{section: "[features]", key: "hooks", value: "true"}, tomlEdit{section: "[features]", key: "plugin_hooks", value: "true"})
	if err == nil {
		t.Fatal("expected unsupported existing value to fail safely")
	}
	content, _ := os.ReadFile(path)
	if string(content) != original {
		t.Fatalf("partially applied configuration: %s", content)
	}
}

func TestTOMLEditsPreserveSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target.toml"), filepath.Join(dir, "config.toml")
	if err := os.WriteFile(target, []byte("[features]\nhooks = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := editTOMLFile(link, tomlEdit{section: "[features]", key: "hooks", value: "true"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced configuration symlink")
	}
	content, _ := os.ReadFile(target)
	if !strings.Contains(string(content), "hooks = true") {
		t.Fatalf("target not updated: %s", content)
	}
	dangling := filepath.Join(dir, "dangling.toml")
	if err := os.Symlink(filepath.Join(dir, "missing.toml"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := editTOMLFile(dangling, tomlEdit{section: "[features]", key: "hooks", value: "true"}); err == nil {
		t.Fatal("expected unresolved configuration symlink to fail safely")
	}
	info, err = os.Lstat(dangling)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("unresolved symlink changed: %v", err)
	}
}

func TestTOMLEditsSupportQuotedSectionsAndDottedKeys(t *testing.T) {
	for _, original := range []string{"[\"features\"] # quoted\n'hooks' = false\n", "features.hooks = false\n[other]\nkeep = true\n"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		if err := editTOMLFile(path, tomlEdit{section: "[features]", key: "hooks", value: "true"}, tomlEdit{section: "[features]", key: "plugin_hooks", value: "true"}); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(path)
		var parsed map[string]any
		if err := toml.Unmarshal(content, &parsed); err != nil {
			t.Fatal(err)
		}
		flags := parsed["features"].(map[string]any)
		if flags["hooks"] != true || flags["plugin_hooks"] != true {
			t.Fatalf("incorrect flags: %v", flags)
		}
	}
}
