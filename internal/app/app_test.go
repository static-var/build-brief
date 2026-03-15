package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"build-brief/internal/gradle"
)

func TestParseArgsStopsAtGradleArgs(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--mode", "raw", "test", "--stacktrace"})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}

	if opts.Mode != "raw" {
		t.Fatalf("expected raw mode, got %s", opts.Mode)
	}

	if opts.DaemonMode != string(gradle.DaemonModeAuto) {
		t.Fatalf("expected auto daemon mode by default, got %s", opts.DaemonMode)
	}

	if len(gradleArgs) != 2 || gradleArgs[0] != "test" || gradleArgs[1] != "--stacktrace" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsTreatsJSONModeAsHuman(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--mode", "json", "test"})
	if err != nil {
		t.Fatalf("parse json compatibility mode: %v", err)
	}

	if opts.Mode != "human" {
		t.Fatalf("expected json mode to normalize to human, got %s", opts.Mode)
	}

	if len(gradleArgs) != 1 || gradleArgs[0] != "test" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsReadsDaemonAndGradleUserHome(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--daemon-mode", "on", "--gradle-user-home", "/tmp/shared-home", "test"})
	if err != nil {
		t.Fatalf("parse daemon args: %v", err)
	}

	if opts.DaemonMode != string(gradle.DaemonModeOn) {
		t.Fatalf("expected daemon mode on, got %s", opts.DaemonMode)
	}

	if opts.GradleUserHome != "/tmp/shared-home" {
		t.Fatalf("expected gradle user home to be parsed, got %s", opts.GradleUserHome)
	}

	if len(gradleArgs) != 1 || gradleArgs[0] != "test" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsRejectsInvalidDaemonMode(t *testing.T) {
	_, _, err := parseArgs([]string{"--daemon-mode", "maybe", "test"})
	if err == nil {
		t.Fatal("expected invalid daemon mode to be rejected")
	}

	if !strings.Contains(err.Error(), "invalid daemon mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsHonorsDelimiter(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--project-dir", "/tmp/project", "--", "--stacktrace", "test"})
	if err != nil {
		t.Fatalf("parse args with delimiter: %v", err)
	}

	if opts.ProjectDir != "/tmp/project" {
		t.Fatalf("unexpected project dir: %s", opts.ProjectDir)
	}

	if len(gradleArgs) != 2 || gradleArgs[0] != "--stacktrace" || gradleArgs[1] != "test" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsRejectsUnknownBuildBriefFlag(t *testing.T) {
	_, _, err := parseArgs([]string{"--stacktrace", "test"})
	if err == nil {
		t.Fatal("expected unknown build-brief flag error")
	}
}

func TestParseArgsReadsVersionFlag(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--version"})
	if err != nil {
		t.Fatalf("parse version flag: %v", err)
	}

	if !opts.Version {
		t.Fatal("expected version flag to be set")
	}

	if len(gradleArgs) != 0 {
		t.Fatalf("expected no gradle args, got %v", gradleArgs)
	}
}

func TestParseArgsRejectsGlobalInstallForce(t *testing.T) {
	_, _, err := parseArgs([]string{"--global", "--install-force"})
	if err == nil {
		t.Fatal("expected --global --install-force to be rejected")
	}

	if !strings.Contains(err.Error(), "--install-force cannot be combined with --global") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsGlobalInstall(t *testing.T) {
	_, _, err := parseArgs([]string{"--global", "--install"})
	if err == nil {
		t.Fatal("expected --global --install to be rejected")
	}

	if !strings.Contains(err.Error(), "--install cannot be combined with --global") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPrintsVersion(t *testing.T) {
	originalVersion := Version
	Version = "test-version"
	t.Cleanup(func() {
		Version = originalVersion
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--version"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	if strings.TrimSpace(stdout.String()) != "build-brief test-version" {
		t.Fatalf("unexpected version output %q", stdout.String())
	}
}

func TestRunPrintsHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--help"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	help := stdout.String()
	for _, expected := range []string{
		"Common commands:",
		"build-brief --global",
		"build-brief gains --history",
		"build-brief gains --reset",
		"build-brief rewrite 'gradle test'",
		"--daemon-mode MODE",
		"BUILD_BRIEF_DAEMON_MODE",
		"Selection accepts comma-separated numbers, '*' or 'all', or blank to cancel.",
		"Only existing global instruction files are updated; OpenCode also installs a managed plugin file.",
		"Must be used by itself; do not combine it with --install or --install-force.",
		"Intended for hooks/plugins such as the OpenCode tool.execute.before hook.",
	} {
		if !strings.Contains(help, expected) {
			t.Fatalf("expected help to contain %q, got %q", expected, help)
		}
	}

	for _, unexpected := range []string{
		"--mode [human|json|raw]",
		"build-brief --mode json build",
	} {
		if strings.Contains(help, unexpected) {
			t.Fatalf("expected help not to contain %q, got %q", unexpected, help)
		}
	}
}

func TestRunRewrite(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"rewrite", "gradle clean"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "build-brief -- clean" {
		t.Fatalf("unexpected rewrite output %q", stdout.String())
	}
}

func TestParseGainsArgs(t *testing.T) {
	opts, err := parseGainsArgs([]string{"--project", "--history", "--format", "json"})
	if err != nil {
		t.Fatalf("parse gains args: %v", err)
	}

	if !opts.Project || !opts.History || opts.Format != "json" {
		t.Fatalf("unexpected gains options: %+v", opts)
	}
}

func TestParseGainsArgsReset(t *testing.T) {
	opts, err := parseGainsArgs([]string{"--reset"})
	if err != nil {
		t.Fatalf("parse gains reset args: %v", err)
	}

	if !opts.Reset || opts.Project || opts.History || opts.Format != "text" {
		t.Fatalf("unexpected reset gains options: %+v", opts)
	}
}

func TestRunInstallsLocalAgentsFile(t *testing.T) {
	originalRTKInstalled := rtkInstalled
	originalRTKInstallNotice := rtkInstallNotice
	originalCurrentDir := currentDir
	rtkInstalled = func() bool { return false }
	rtkInstallNotice = func() string { return "unused" }
	tempDir := t.TempDir()
	currentDir = func() (string, error) { return tempDir, nil }
	t.Cleanup(func() {
		rtkInstalled = originalRTKInstalled
		rtkInstallNotice = originalRTKInstallNotice
		currentDir = originalCurrentDir
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--install-force"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}

	content, err := os.ReadFile(filepath.Join(tempDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}

	if !strings.Contains(string(content), "build-brief") {
		t.Fatalf("expected build-brief instructions in AGENTS.md, got %q", string(content))
	}

	if strings.Contains(stdout.String(), "RTK detected on this machine") {
		t.Fatalf("expected no RTK notice when RTK is not detected, got %q", stdout.String())
	}
}

func TestRunLocalInstallPrintsRTKNoticeWhenDetected(t *testing.T) {
	originalRTKInstalled := rtkInstalled
	originalRTKInstallNotice := rtkInstallNotice
	originalCurrentDir := currentDir
	rtkInstalled = func() bool { return true }
	rtkInstallNotice = func() string { return "RTK note for tests" }
	tempDir := t.TempDir()
	currentDir = func() (string, error) { return tempDir, nil }
	t.Cleanup(func() {
		rtkInstalled = originalRTKInstalled
		rtkInstallNotice = originalRTKInstallNotice
		currentDir = originalCurrentDir
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--install-force"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}

	if !strings.Contains(stdout.String(), "RTK note for tests") {
		t.Fatalf("expected RTK install notice in stdout, got %q", stdout.String())
	}
}

func TestRunRawModeStreamsOutput(t *testing.T) {
	projectDir := t.TempDir()
	scriptPath := filepath.Join(t.TempDir(), "fake-gradle.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho raw-line-1\necho raw-line-2\n"), 0o755); err != nil {
		t.Fatalf("write fake gradle: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"--mode", "raw",
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		"test",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}

	if stdout.String() != "raw-line-1\nraw-line-2\n" {
		t.Fatalf("unexpected raw mode output %q", stdout.String())
	}
}
