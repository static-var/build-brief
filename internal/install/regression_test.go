package install

import (
	"os"
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
