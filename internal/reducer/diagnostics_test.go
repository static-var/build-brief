package reducer

import (
	"strings"
	"testing"
	"time"

	"build-brief/internal/gradle"
	"build-brief/internal/runner"
)

func TestDiagnoseKotlinDaemonFailure(t *testing.T) {
	summary := reduceDiagnosticLog(t, []string{
		"> Task :app:compileDebugKotlin FAILED",
		"e: Daemon compilation failed: Could not connect to Kotlin compile daemon",
		"FAILURE: Build failed with an exception.",
		"* What went wrong:",
		"Execution failed for task ':app:compileDebugKotlin'.",
		"BUILD FAILED in 14s",
	})

	diagnostic := requireDiagnostic(t, summary, "kotlin_daemon_failure")
	if diagnostic.Summary != "Kotlin compiler daemon failure" {
		t.Fatalf("summary = %q", diagnostic.Summary)
	}
	assertEvidenceContains(t, diagnostic, "Could not connect to Kotlin compile daemon")
	assertEvidenceContains(t, diagnostic, "Daemon compilation failed")
	assertNextCheckContains(t, diagnostic, "Inspect Gradle/Kotlin JVM args")
	assertNextCheckContains(t, diagnostic, "./gradlew --stop")
	assertNextCheckContains(t, diagnostic, "then retry build-brief")
	assertNextCheckContains(t, diagnostic, "./gradlew --no-daemon <task>")
	for _, nextCheck := range diagnostic.NextChecks {
		if strings.Contains(nextCheck, "build-brief --no-daemon") {
			t.Fatalf("diagnostic recommends impossible build-brief daemon override: %q", nextCheck)
		}
	}
}

func TestDiagnoseAndroidSDKLicenseFailure(t *testing.T) {
	summary := reduceDiagnosticLog(t, []string{
		"> Task :app:compileDebugJavaWithJavac FAILED",
		"Failed to install the following Android SDK packages as some licences have not been accepted.",
		"build-tools;35.0.0 Android SDK Build-Tools 35",
		"BUILD FAILED in 2s",
	})

	diagnostic := requireDiagnostic(t, summary, "android_sdk_license")
	assertEvidenceContains(t, diagnostic, "licences have not been accepted")
	assertNextCheckContains(t, diagnostic, "Accept Android SDK licenses")
}

func TestDiagnoseDependencyResolutionFailure(t *testing.T) {
	summary := reduceDiagnosticLog(t, []string{
		"> Could not resolve all files for configuration ':app:debugRuntimeClasspath'.",
		"> Could not find com.example:missing-lib:1.0.",
		"Searched in the following locations:",
		"BUILD FAILED in 1s",
	})

	diagnostic := requireDiagnostic(t, summary, "dependency_resolution_failure")
	assertEvidenceContains(t, diagnostic, "Could not resolve all files")
	assertEvidenceContains(t, diagnostic, "Could not find com.example:missing-lib:1.0")
	assertNextCheckContains(t, diagnostic, "dependency coordinates")
}

func TestDiagnosticPriorityKeepsAndroidSDKBeforeKotlin(t *testing.T) {
	summary := reduceDiagnosticLog(t, []string{
		"SDK location not found. Define a valid SDK location with an ANDROID_HOME environment variable.",
		"e: Daemon compilation failed: Could not connect to Kotlin compile daemon",
		"BUILD FAILED in 1s",
	})

	if len(summary.Diagnostics) < 2 {
		t.Fatalf("expected multiple diagnostics, got %+v", summary.Diagnostics)
	}
	if summary.Diagnostics[0].ID != "android_sdk_missing" {
		t.Fatalf("top diagnostic = %q, want android_sdk_missing", summary.Diagnostics[0].ID)
	}
}

func reduceDiagnosticLog(t *testing.T, lines []string) Summary {
	t.Helper()
	summary, err := Reduce(
		gradle.Command{Executable: "/tmp/gradlew", Args: []string{"--console=plain", "build"}, ProjectDir: "/tmp/project", Source: gradle.SourceWrapper},
		runner.Result{ExitCode: 1, Duration: time.Second, RawLogPath: writeTestLog(t, lines)},
	)
	if err != nil {
		t.Fatalf("reduce: %v", err)
	}
	return summary
}

func requireDiagnostic(t *testing.T, summary Summary, id string) Diagnostic {
	t.Helper()
	for _, diagnostic := range summary.Diagnostics {
		if diagnostic.ID == id {
			return diagnostic
		}
	}
	t.Fatalf("missing diagnostic %q in %+v", id, summary.Diagnostics)
	return Diagnostic{}
}

func assertEvidenceContains(t *testing.T, diagnostic Diagnostic, want string) {
	t.Helper()
	for _, evidence := range diagnostic.Evidence {
		if strings.Contains(evidence, want) {
			return
		}
	}
	t.Fatalf("diagnostic %s evidence %v does not contain %q", diagnostic.ID, diagnostic.Evidence, want)
}

func assertNextCheckContains(t *testing.T, diagnostic Diagnostic, want string) {
	t.Helper()
	for _, nextCheck := range diagnostic.NextChecks {
		if strings.Contains(nextCheck, want) {
			return
		}
	}
	t.Fatalf("diagnostic %s next checks %v do not contain %q", diagnostic.ID, diagnostic.NextChecks, want)
}

func TestDiagnoseBasicCategories(t *testing.T) {
	cases := []struct {
		name string
		id   string
		log  []string
	}{
		{
			name: "kotlin oom",
			id:   "kotlin_oom",
			log:  []string{"Execution failed for task ':app:compileDebugKotlin'.", "java.lang.OutOfMemoryError: Java heap space", "BUILD FAILED in 1s"},
		},
		{
			name: "agp error",
			id:   "android_gradle_plugin_error",
			log:  []string{"Android Gradle plugin requires Java 17 to run. You are currently using Java 11.", "BUILD FAILED in 1s"},
		},
		{
			name: "configuration cache",
			id:   "configuration_cache_failure",
			log:  []string{"Configuration cache problems found in this build.", "BUILD FAILED in 1s"},
		},
		{
			name: "lint",
			id:   "lint_failure",
			log:  []string{"> Task :app:lintDebug FAILED", "Lint found fatal errors while assembling a release target.", "BUILD FAILED in 1s"},
		},
		{
			name: "flaky test",
			id:   "flaky_test_failure",
			log:  []string{"ExampleTest > sometimesWorks FAILED", "java.net.SocketTimeoutException: timeout", "BUILD FAILED in 1s"},
		},
		{
			name: "generic test",
			id:   "test_failure",
			log:  []string{"ExampleTest > works FAILED", "BUILD FAILED in 1s"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary := reduceDiagnosticLog(t, tc.log)
			requireDiagnostic(t, summary, tc.id)
		})
	}
}

func TestDoesNotDiagnoseBenignConfigurationCacheStatusAsFailure(t *testing.T) {
	summary := reduceDiagnosticLog(t, []string{
		"Configuration cache entry reused.",
		"ExampleTest > works FAILED",
		"BUILD FAILED in 1s",
	})

	if hasDiagnostic(summary, "configuration_cache_failure") {
		t.Fatalf("did not expect configuration cache diagnostic in %+v", summary.Diagnostics)
	}
	requireDiagnostic(t, summary, "test_failure")
}

func TestDoesNotDiagnoseNonTestTimeoutAsFlakyTest(t *testing.T) {
	summary := reduceDiagnosticLog(t, []string{
		"> Task :app:mergeDebugResources FAILED",
		"Execution failed for task ':app:mergeDebugResources'.",
		"java.net.ConnectException: connection refused while contacting remote service",
		"BUILD FAILED in 1s",
	})

	if hasDiagnostic(summary, "flaky_test_failure") {
		t.Fatalf("did not expect flaky test diagnostic in %+v", summary.Diagnostics)
	}
}

func hasDiagnostic(summary Summary, id string) bool {
	for _, diagnostic := range summary.Diagnostics {
		if diagnostic.ID == id {
			return true
		}
	}
	return false
}

func TestGenericTestDiagnosticUsesImportantFailureDetails(t *testing.T) {
	summary := reduceDiagnosticLog(t, []string{
		"ExampleTest > returnsValue FAILED",
		"FAILURE: Build failed with an exception.",
		"* What went wrong:",
		"expected:<Hello> but was:<null>",
		"at com.example.ExampleTest.returnsValue(ExampleTest.kt:42)",
		"BUILD FAILED in 1s",
	})

	diagnostic := requireDiagnostic(t, summary, "test_failure")
	assertEvidenceContains(t, diagnostic, "expected:<Hello> but was:<null>")
}

func TestDoesNotClassifyJavaOOMAsKotlinOOMWithoutKotlinContext(t *testing.T) {
	summary := reduceDiagnosticLog(t, []string{
		"> Task :test FAILED",
		"java.lang.OutOfMemoryError: Java heap space",
		"BUILD FAILED in 1s",
	})

	if hasDiagnostic(summary, "kotlin_oom") {
		t.Fatalf("did not expect kotlin oom diagnostic in %+v", summary.Diagnostics)
	}
}

func TestDoesNotClassifyGradleDSLMissingMethodAsDependencyResolution(t *testing.T) {
	summary := reduceDiagnosticLog(t, []string{
		"A problem occurred evaluating root project 'sample'.",
		"Could not find method implementation() for arguments [com.example:lib:1.0] on object of type org.gradle.api.internal.artifacts.dsl.dependencies.DefaultDependencyHandler.",
		"BUILD FAILED in 1s",
	})

	if hasDiagnostic(summary, "dependency_resolution_failure") {
		t.Fatalf("did not expect dependency diagnostic in %+v", summary.Diagnostics)
	}
}
