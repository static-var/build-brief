package buildbrief_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseWorkflowDefaultsToDryRunAndGatesPublication(t *testing.T) {
	workflow := readTestFile(t, ".github/workflows/release.yml")

	if got := strings.Count(workflow, "default: false\n        type: boolean"); got != 2 {
		t.Fatalf("expected publish inputs to default false, found %d defaults", got)
	}

	for _, want := range []string{
		"publish:\n        description:",
		"publish_homebrew:\n        description:",
		"if: inputs.publish && steps.prepare.outputs.needs_commit == 'true'",
		"if: inputs.publish && steps.prepare.outputs.tag_exists != 'true'",
		"if: inputs.publish\n        env:\n          GH_TOKEN:",
		"if: inputs.publish && inputs.publish_homebrew",
		"actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2",
		"actions/setup-go@d35c59abb061a4a6fb18e82ac0862c26744d6ab5 # v5.5.0",
		"actions/setup-python@a26af69be951a213d495a4c3e4e4022e16d87065 # v5.6.0",
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release workflow missing %q", want)
		}
	}
}

func TestPublishHomebrewTapFailsWhenTokenIsMissing(t *testing.T) {
	dir := t.TempDir()
	formulaFile := filepath.Join(dir, "build-brief.rb")
	writeTestFile(t, formulaFile, "class BuildBrief < Formula\nend\n")

	cmd := exec.Command("bash", "scripts/publish-homebrew-tap.sh",
		"--tap-repo", "example/homebrew-tap",
		"--formula-file", formulaFile,
		"--version", "0.0.1",
	)
	cmd.Env = append(os.Environ(), "HOMEBREW_TAP_TOKEN=")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("publish-homebrew-tap.sh unexpectedly succeeded without a token:\n%s", output)
	}
	if !strings.Contains(string(output), "HOMEBREW_TAP_TOKEN is not set") {
		t.Fatalf("expected missing-token error, got:\n%s", output)
	}
}

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
