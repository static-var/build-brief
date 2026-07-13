package reducer

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"build-brief/internal/gradle"
	"build-brief/internal/runner"
)

func TestReduceRepresentativeFixtures(t *testing.T) {
	t.Parallel()

	type expectation struct {
		exitCode   int
		success    bool
		failedTask string
		failedTest string
		warnings   int
	}

	fixtures := map[string]expectation{
		"android-failure.log": {
			exitCode:   1,
			success:    false,
			failedTask: ":app:testDebugUnitTest",
			failedTest: "MainViewModelTest > loadsData",
			warnings:   2,
		},
		"ktor-failure.log": {
			exitCode:   1,
			success:    false,
			failedTask: ":server:test",
			failedTest: "ApplicationTest > healthCheck",
		},
		"kmp-success.log": {
			exitCode: 0,
			success:  true,
		},
		"spring-success.log": {
			exitCode: 0,
			success:  true,
		},
	}

	for name, want := range fixtures {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			summary, err := Reduce(
				gradle.Command{
					Executable: "/tmp/gradlew",
					Args:       []string{"--console=plain", "build"},
					ProjectDir: "/tmp/project",
					Source:     gradle.SourceWrapper,
				},
				runner.Result{
					ExitCode:   want.exitCode,
					Duration:   2 * time.Second,
					RawLogPath: filepath.Join("testdata", name),
				},
			)
			if err != nil {
				t.Fatalf("reduce fixture %s: %v", name, err)
			}

			if summary.Success != want.success {
				t.Fatalf("expected success=%v, got %v", want.success, summary.Success)
			}

			if want.failedTask != "" && !contains(summary.FailedTasks, want.failedTask) {
				t.Fatalf("expected failed task %q in %v", want.failedTask, summary.FailedTasks)
			}

			if want.failedTest != "" && !contains(summary.FailedTests, want.failedTest) {
				t.Fatalf("expected failed test %q in %v", want.failedTest, summary.FailedTests)
			}

			if summary.WarningCount != want.warnings {
				t.Fatalf("expected %d warnings, got %d", want.warnings, summary.WarningCount)
			}
		})
	}
}

func TestReducePhase4QualityCorpus(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		name      string
		required  []string
		forbidden []string
	}{
		{
			name: "android-kotlin-compiler-noise-ansi.log",
			required: []string{
				":app:compileDebugKotlin",
				"DashboardViewModel.kt:42:17: error: unresolved reference: sessionToken",
				"unresolved reference: sessionToken",
			},
			forbidden: []string{"\x1b[", "dependency_resolution_failure"},
		},
		{
			name: "dependency-resolution-failure.log",
			required: []string{
				":app:mergeDebugRuntimeClasspath",
				"Could not find com.example.sanitized:telemetry-client:9.9.9",
				"dependency_resolution_failure",
			},
			forbidden: []string{"configuration_cache_failure", "kotlin_daemon_failure"},
		},
		{
			name: "configuration-cache-failure.log",
			required: []string{
				"1 problem was found storing the configuration cache.",
				"configuration_cache_failure",
			},
			forbidden: []string{"dependency_resolution_failure", "android_gradle_plugin_error"},
		},
		{
			name: "plugin-resolution-unclassified.log",
			required: []string{
				"Plugin [id: 'com.example.sanitized.conventions', version: '1.4.0'] was not found in any of the following sources:",
			},
			forbidden: []string{
				"dependency_resolution_failure",
				"configuration_cache_failure",
				"android_gradle_plugin_error",
			},
		},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()

			summary, err := Reduce(
				gradle.Command{Executable: "/tmp/gradlew", Args: []string{"--console=plain", "build"}, ProjectDir: "/tmp/project", Source: gradle.SourceWrapper},
				runner.Result{ExitCode: 1, Duration: time.Second, RawLogPath: filepath.Join("testdata", fixture.name)},
			)
			if err != nil {
				t.Fatalf("reduce fixture: %v", err)
			}
			if summary.Success {
				t.Fatal("expected failed summary")
			}

			text := fixtureContractText(summary)
			for _, required := range fixture.required {
				required := required
				t.Run("required/"+required, func(t *testing.T) {
					if !strings.Contains(text, required) {
						t.Fatalf("required contract %q missing from:\n%s", required, text)
					}
				})
			}
			for _, forbidden := range fixture.forbidden {
				forbidden := forbidden
				t.Run("forbidden/"+forbidden, func(t *testing.T) {
					if strings.Contains(text, forbidden) {
						t.Fatalf("forbidden contract %q found in:\n%s", forbidden, text)
					}
				})
			}
		})
	}
}

func fixtureContractText(summary Summary) string {
	lines := append([]string{}, summary.FailedTasks...)
	lines = append(lines, summary.ImportantLines...)
	lines = append(lines, summary.ConfigCacheProblems...)
	for _, diagnostic := range summary.Diagnostics {
		lines = append(lines, diagnostic.ID)
		lines = append(lines, diagnostic.Evidence...)
	}
	return strings.Join(lines, "\n")
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
