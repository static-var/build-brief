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

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

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
	args := ApplyStableArgs([]string{"test", "--stacktrace"}, StableArgsOptions{})
	if len(args) == 0 || args[0] != "--console=plain" {
		t.Fatalf("expected --console=plain to be prepended, got %v", args)
	}
}

func TestApplyStableArgsRespectsExistingConsoleFlag(t *testing.T) {
	args := ApplyStableArgs([]string{"--console=rich", "test"}, StableArgsOptions{})
	if len(args) == 0 || args[0] != "--console=rich" {
		t.Fatalf("expected existing console flag to be preserved, got %v", args)
	}
}

func TestApplyStableArgsAddsDaemonFlagWhenRequested(t *testing.T) {
	args := ApplyStableArgs([]string{"test"}, StableArgsOptions{DaemonMode: DaemonModeOn})

	if len(args) < 2 || args[1] != "--daemon" {
		t.Fatalf("expected --daemon to be injected after console flag, got %v", args)
	}
}

func TestApplyStableArgsAddsNoDaemonFlagWhenRequested(t *testing.T) {
	args := ApplyStableArgs([]string{"test"}, StableArgsOptions{DaemonMode: DaemonModeOff})

	if len(args) < 2 || args[1] != "--no-daemon" {
		t.Fatalf("expected --no-daemon to be injected after console flag, got %v", args)
	}
}

func TestApplyStableArgsRespectsExistingDaemonFlag(t *testing.T) {
	args := ApplyStableArgs([]string{"--no-daemon", "test"}, StableArgsOptions{DaemonMode: DaemonModeOn})

	for _, arg := range args {
		if arg == "--daemon" {
			t.Fatalf("did not expect --daemon when explicit daemon flag already exists, got %v", args)
		}
	}
}

func TestApplyStableArgsAddsGradleUserHome(t *testing.T) {
	args := ApplyStableArgs([]string{"test"}, StableArgsOptions{GradleUserHome: "/tmp/shared-gradle-home"})

	if len(args) < 3 || args[1] != "--gradle-user-home" || args[2] != "/tmp/shared-gradle-home" {
		t.Fatalf("expected --gradle-user-home to be injected, got %v", args)
	}
}

func TestApplyStableArgsRespectsExistingGradleUserHome(t *testing.T) {
	args := ApplyStableArgs([]string{"--gradle-user-home", "/tmp/existing", "test"}, StableArgsOptions{GradleUserHome: "/tmp/shared-gradle-home"})

	found := 0
	for _, arg := range args {
		if arg == "--gradle-user-home" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("expected a single --gradle-user-home flag, got %v", args)
	}
}
