package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestParseArgsStopsAtGradleArgs(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--mode", "json", "test", "--stacktrace"})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}

	if opts.Mode != "json" {
		t.Fatalf("expected json mode, got %s", opts.Mode)
	}

	if len(gradleArgs) != 2 || gradleArgs[0] != "test" || gradleArgs[1] != "--stacktrace" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
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

func TestRunPrintsVersion(t *testing.T) {
	originalVersion := Version
	Version = "test-version"
	t.Cleanup(func() {
		Version = originalVersion
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--version"}, &stdout, &stderr)
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
