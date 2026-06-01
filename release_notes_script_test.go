package buildbrief_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendGeneratedReleaseNotesCombinesBaseAndGenerated(t *testing.T) {
	dir := t.TempDir()
	baseFile := filepath.Join(dir, "release-notes.md")
	generatedFile := filepath.Join(dir, "generated-notes.md")
	outputFile := filepath.Join(dir, "combined-notes.md")

	base := "## v0.0.12 - 2026-06-02\n\n- Fix installer release downloads (e3c9ed3)\n"
	generated := "## What's Changed\n* Fix installer release downloads by @static-var in https://github.com/static-var/build-brief/pull/16\n\n**Full Changelog**: https://github.com/static-var/build-brief/compare/v0.0.11...v0.0.12\n"
	writeTestFile(t, baseFile, base)
	writeTestFile(t, generatedFile, generated)

	runAppendGeneratedReleaseNotes(t, baseFile, generatedFile, outputFile)

	got := readTestFile(t, outputFile)
	want := base + "\n## Auto-generated changelog\n\n" + generated
	if got != want {
		t.Fatalf("combined release notes mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestAppendGeneratedReleaseNotesKeepsBaseWhenGeneratedIsEmpty(t *testing.T) {
	dir := t.TempDir()
	baseFile := filepath.Join(dir, "release-notes.md")
	generatedFile := filepath.Join(dir, "generated-notes.md")
	outputFile := filepath.Join(dir, "combined-notes.md")

	base := "## v0.0.12 - 2026-06-02\n\n- Fix installer release downloads (e3c9ed3)\n"
	writeTestFile(t, baseFile, base)
	writeTestFile(t, generatedFile, "\n\n")

	runAppendGeneratedReleaseNotes(t, baseFile, generatedFile, outputFile)

	got := readTestFile(t, outputFile)
	if got != base {
		t.Fatalf("expected base notes only, got:\n%s", got)
	}
}

func runAppendGeneratedReleaseNotes(t *testing.T, baseFile, generatedFile, outputFile string) {
	t.Helper()

	cmd := exec.Command("bash", "scripts/append-generated-release-notes.sh",
		"--notes-file", baseFile,
		"--generated-file", generatedFile,
		"--output", outputFile,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("append-generated-release-notes.sh failed: %v\n%s", err, output)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}
