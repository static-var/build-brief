package gradle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePrefersWrapper(t *testing.T) {
	projectDir := t.TempDir()
	wrapperPath := filepath.Join(projectDir, "gradlew")

	if err := os.WriteFile(wrapperPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	command, err := Resolve(projectDir, "", "")
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

	command, err := Resolve(projectDir, "", "")
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

func TestApplyStableArgsNormalizesExistingConsoleFlag(t *testing.T) {
	args := ApplyStableArgs([]string{"--console=plain", "test"}, StableArgsOptions{})
	foundConsole := 0
	for _, arg := range args {
		if arg == "--console=plain" {
			foundConsole++
		}
	}
	if foundConsole != 1 {
		t.Fatalf("expected a single plain console flag, got %v", args)
	}
}

func TestApplyStableArgsStripsDaemonFlags(t *testing.T) {
	args := ApplyStableArgs([]string{"--daemon", "--no-daemon", "test"}, StableArgsOptions{})
	for _, arg := range args {
		if arg == "--daemon" || arg == "--no-daemon" {
			t.Fatalf("expected daemon flags to be stripped, got %v", args)
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

func TestTrackingLineRedactsSecretFlags(t *testing.T) {
	command := Command{
		Executable: "/tmp/gradlew",
		Args: []string{
			"--console=plain",
			"--gradle-user-home", "/tmp/gradle-home",
			"test",
			"--stacktrace",
			"--tests", "com.example.SecretTest",
			"-Psigning.keyId=ABC123",
			"-Ddb.password=secret",
			"--project-prop", "token=secret",
			"--system-prop", "password=secret",
			"--scan",
			"--unknown-flag",
		},
	}

	got := command.TrackingLine()

	if got != "gradlew test --stacktrace --tests com.example.SecretTest -P<redacted> -D<redacted> --project-prop <redacted> --system-prop <redacted> --scan" {
		t.Fatalf("unexpected tracking line: %q", got)
	}
}

func TestTrackingLineKeepsEqualsFormTaskSelectors(t *testing.T) {
	command := Command{
		Executable: "/tmp/gradlew",
		Args: []string{
			"test",
			"--tests=com.example.SecretTest",
			"--exclude-task=lint",
		},
	}

	got := command.TrackingLine()

	if got != "gradlew test --tests=com.example.SecretTest --exclude-task=lint" {
		t.Fatalf("unexpected tracking line: %q", got)
	}
}

func TestSplitInvocationRecognizesGradleExecutable(t *testing.T) {
	invocation, args := SplitInvocation([]string{"gradle", "test"})
	if invocation != "gradle" {
		t.Fatalf("expected gradle invocation, got %q", invocation)
	}
	if len(args) != 1 || args[0] != "test" {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestSplitInvocationRecognizesWrapperPath(t *testing.T) {
	invocation, args := SplitInvocation([]string{"./gradlew", "--stacktrace", "test"})
	if invocation != "./gradlew" {
		t.Fatalf("expected wrapper invocation, got %q", invocation)
	}
	if len(args) != 2 || args[0] != "--stacktrace" || args[1] != "test" {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestSplitInvocationLeavesTaskOnlyArgsAlone(t *testing.T) {
	invocation, args := SplitInvocation([]string{"test", "--stacktrace"})
	if invocation != "" {
		t.Fatalf("expected no invocation override, got %q", invocation)
	}
	if len(args) != 2 || args[0] != "test" || args[1] != "--stacktrace" {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestValidateArgsRejectsQuietFlag(t *testing.T) {
	if err := ValidateArgs([]string{"--quiet", "test"}); err != nil {
		t.Fatalf("expected quiet flag to be accepted and sanitized later, got %v", err)
	}
}

func TestValidateArgsAllowsConsoleOverrideForSanitization(t *testing.T) {
	if err := ValidateArgs([]string{"--console=rich", "test"}); err != nil {
		t.Fatalf("expected console override to be sanitized later, got %v", err)
	}
}

func TestValidateArgsAllowsWarningModeForSanitization(t *testing.T) {
	if err := ValidateArgs([]string{"--warning-mode=none", "test"}); err != nil {
		t.Fatalf("expected warning-mode to be sanitized later, got %v", err)
	}
}

func TestValidateArgsAllowsPlainConsole(t *testing.T) {
	if err := ValidateArgs([]string{"--console=plain", "test"}); err != nil {
		t.Fatalf("expected plain console to be allowed, got %v", err)
	}
}

func TestValidateArgsRejectsMissingConsoleValue(t *testing.T) {
	err := ValidateArgs([]string{"--console"})
	if err == nil || !strings.Contains(err.Error(), "missing value") {
		t.Fatalf("expected missing console value error, got %v", err)
	}
}

func TestApplyStableArgsSanitizesOutputFlags(t *testing.T) {
	args := ApplyStableArgs([]string{"--quiet", "--warn", "--warning-mode=all", "--console=rich", "--plain-text", "test"}, StableArgsOptions{})
	got := strings.Join(args, " ")
	for _, unexpected := range []string{"--quiet", "--warn", "--warning-mode=all", "--console=rich", "--plain-text"} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("expected %q to be stripped from %v", unexpected, args)
		}
	}
	if !strings.Contains(got, "--console=plain") || !strings.Contains(got, "test") {
		t.Fatalf("expected sanitized args to keep plain console and task, got %v", args)
	}
}

func TestResolveExplicitGradleRelativePathUsesCurrentWorkingDirectory(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	cwd := t.TempDir()
	projectDir := t.TempDir()
	explicitDir := filepath.Join(cwd, "tools")
	if err := os.MkdirAll(explicitDir, 0o755); err != nil {
		t.Fatalf("mkdir explicit dir: %v", err)
	}

	explicitPath := filepath.Join(explicitDir, "gradle")
	if err := os.WriteFile(explicitPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake gradle: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir cwd: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	command, err := Resolve(projectDir, "./tools/gradle", "")
	if err != nil {
		t.Fatalf("resolve explicit gradle: %v", err)
	}

	if command.Source != SourceExplicit {
		t.Fatalf("expected explicit source, got %s", command.Source)
	}
	wantInfo, err := os.Stat(explicitPath)
	if err != nil {
		t.Fatalf("stat expected executable: %v", err)
	}
	gotInfo, err := os.Stat(command.Executable)
	if err != nil {
		t.Fatalf("stat resolved executable: %v", err)
	}
	if !os.SameFile(wantInfo, gotInfo) {
		t.Fatalf("expected executable pointing to %q, got %q", explicitPath, command.Executable)
	}
	if command.ProjectDir != projectDir {
		t.Fatalf("expected project dir %q, got %q", projectDir, command.ProjectDir)
	}
}
