package gradle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersWrapper(t *testing.T) {
	projectDir := t.TempDir()
	wrapperPath := filepath.Join(projectDir, "gradlew")

	if err := os.WriteFile(wrapperPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	command, err := Resolve(projectDir, "")
	if err != nil {
		t.Fatalf("resolve wrapper: %v", err)
	}

	if command.Source != SourceWrapper {
		t.Fatalf("expected wrapper source, got %s", command.Source)
	}

	if command.Executable != wrapperPath {
		t.Fatalf("expected %s, got %s", wrapperPath, command.Executable)
	}
}

func TestResolveFallsBackToSystemGradle(t *testing.T) {
	projectDir := t.TempDir()
	binDir := t.TempDir()
	gradlePath := filepath.Join(binDir, "gradle")

	if err := os.WriteFile(gradlePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake gradle: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Cleanup(func() {
		_ = os.Setenv("PATH", originalPath)
	})

	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}

	command, err := Resolve(projectDir, "")
	if err != nil {
		t.Fatalf("resolve system gradle: %v", err)
	}

	if command.Source != SourceSystem {
		t.Fatalf("expected system source, got %s", command.Source)
	}

	if command.Executable != gradlePath {
		t.Fatalf("expected %s, got %s", gradlePath, command.Executable)
	}
}

func TestApplyStableArgsAddsPlainConsole(t *testing.T) {
	args := ApplyStableArgs([]string{"test", "--stacktrace"})
	if len(args) == 0 || args[0] != "--console=plain" {
		t.Fatalf("expected --console=plain to be prepended, got %v", args)
	}
}

func TestApplyStableArgsRespectsExistingConsoleFlag(t *testing.T) {
	args := ApplyStableArgs([]string{"--console=rich", "test"})
	if len(args) == 0 || args[0] != "--console=rich" {
		t.Fatalf("expected existing console flag to be preserved, got %v", args)
	}
}
