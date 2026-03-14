package reducer

import (
	"path/filepath"
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

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
