package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunReportsProjectDirFailure(t *testing.T) {
	report := Run(Options{ProjectDir: filepath.Join(t.TempDir(), "missing")})

	result := findResult(t, report, "project-dir")
	if result.Status != StatusFail {
		t.Fatalf("status = %s, want %s", result.Status, StatusFail)
	}
}

func TestRunAllowsMissingDefaultConfigButFailsExplicitMissingConfig(t *testing.T) {
	projectDir := t.TempDir()

	defaultReport := Run(Options{ProjectDir: projectDir})
	if result := findResult(t, defaultReport, "config"); result.Status == StatusFail {
		t.Fatalf("default config status = %s, want non-fail: %+v", result.Status, result)
	}

	explicitReport := Run(Options{ProjectDir: projectDir, ConfigPath: filepath.Join(projectDir, "missing.json")})
	result := findResult(t, explicitReport, "config")
	if result.Status != StatusFail {
		t.Fatalf("explicit config status = %s, want %s", result.Status, StatusFail)
	}
}

func TestRunWarnsWhenGradleMarkersAreAbsent(t *testing.T) {
	projectDir := t.TempDir()

	report := Run(Options{ProjectDir: projectDir})
	result := findResult(t, report, "gradle-markers")
	if result.Status != StatusWarn {
		t.Fatalf("status = %s, want %s", result.Status, StatusWarn)
	}
}

func TestRunChecksWrapperHealthWithoutExecutingGradle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix executable bit check")
	}
	projectDir := t.TempDir()
	gradlew := filepath.Join(projectDir, "gradlew")
	if err := os.WriteFile(gradlew, []byte("#!/bin/sh\nexit 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := Run(Options{ProjectDir: projectDir})

	wrapper := findResult(t, report, "wrapper-health")
	if wrapper.Status != StatusFail {
		t.Fatalf("wrapper status = %s, want %s", wrapper.Status, StatusFail)
	}
	if !strings.Contains(strings.Join(wrapper.Detail, "\n"), "executable") {
		t.Fatalf("wrapper details %q do not mention executable bit", wrapper.Detail)
	}
	resolution := findResult(t, report, "gradle-resolution")
	if resolution.Status != StatusFail {
		t.Fatalf("resolution status = %s, want %s", resolution.Status, StatusFail)
	}
}

func TestRunChecksLogDirAndGradleUserHome(t *testing.T) {
	projectDir := t.TempDir()
	filePath := filepath.Join(projectDir, "not-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := Run(Options{ProjectDir: projectDir, LogDir: filePath, GradleUserHome: filePath})

	if result := findResult(t, report, "log-dir"); result.Status != StatusFail {
		t.Fatalf("log dir status = %s, want %s", result.Status, StatusFail)
	}
	if result := findResult(t, report, "gradle-user-home"); result.Status != StatusFail {
		t.Fatalf("gradle user home status = %s, want %s", result.Status, StatusFail)
	}
}

func TestRunChecksInstallHealth(t *testing.T) {
	projectDir := t.TempDir()

	report := Run(Options{ProjectDir: projectDir, Version: "test-version"})

	result := findResult(t, report, "install-health")
	if result.Status != StatusPass && result.Status != StatusWarn {
		t.Fatalf("install status = %s, want ok or warn", result.Status)
	}
	if !strings.Contains(result.Summary+strings.Join(result.Detail, "\n"), "test-version") {
		t.Fatalf("install result %+v does not include version", result)
	}
}

func TestRenderHumanIncludesStatusesAndMessages(t *testing.T) {
	report := Report{Results: []Result{{Group: "Project", Name: "project directory", Status: StatusPass, Summary: "project directory exists"}}}

	out := RenderHuman(report)

	if !strings.Contains(out, "PASS") || !strings.Contains(out, "project directory exists") {
		t.Fatalf("rendered output %q missing status/message", out)
	}
}

func findResult(t *testing.T, report Report, check string) Result {
	t.Helper()
	aliases := map[string]string{
		"project-dir":       "project directory",
		"gradle-markers":    "Gradle markers",
		"wrapper-health":    "wrapper health",
		"gradle-resolution": "resolution",
		"log-dir":           "log directory",
		"gradle-user-home":  "Gradle user home",
		"install-health":    "version",
		"config":            "default config",
	}
	want := check
	if alias, ok := aliases[check]; ok {
		want = alias
	}
	for _, result := range report.Results {
		if result.Name == want || result.Name == check || strings.Contains(strings.ToLower(result.Name), strings.ToLower(check)) {
			return result
		}
	}
	t.Fatalf("missing result %q in %+v", check, report.Results)
	return Result{}
}

func TestRunTreatsJSONModeAsHumanCompatibility(t *testing.T) {
	report := Run(Options{ProjectDir: t.TempDir(), Mode: "json"})

	result := findResult(t, report, "mode")
	if result.Status != StatusPass || result.Summary != "human" {
		t.Fatalf("mode result = %+v, want PASS human", result)
	}
}
