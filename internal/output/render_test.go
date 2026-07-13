package output

import (
	"bytes"
	"strings"
	"testing"

	"build-brief/internal/reducer"
)

func TestRenderHumanKeepsSuccessConcise(t *testing.T) {
	summary := reducer.Summary{
		Success:         true,
		BuildStatusLine: "BUILD SUCCESSFUL in 5s",
		CommandLine:     "gradle test",
		RawLogPath:      "/tmp/raw.log",
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render success output: %v", err)
	}

	rendered := out.String()
	if strings.TrimSpace(rendered) != "BUILD SUCCESSFUL in 5s" {
		t.Fatalf("unexpected success output %q", rendered)
	}
}

func TestRenderHumanShowsInformationalReportOnSuccess(t *testing.T) {
	summary := reducer.Summary{
		Success:         true,
		BuildStatusLine: "BUILD SUCCESSFUL in 1s",
		CommandLine:     "gradle tasks",
		ReportLines: []string{
			"> Task :tasks",
			"Tasks runnable from root project 'sample'",
			"Build tasks",
			"assemble - Assembles the outputs of this project.",
		},
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render success output with report: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"> Task :tasks",
		"Tasks runnable from root project 'sample'",
		"assemble - Assembles the outputs of this project.",
		"BUILD SUCCESSFUL in 1s",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected report output to contain %q, got %q", expected, rendered)
		}
	}
	if strings.Index(rendered, "> Task :tasks") > strings.Index(rendered, "BUILD SUCCESSFUL in 1s") {
		t.Fatalf("expected report body before status, got %q", rendered)
	}
}

func TestRenderHumanShowsHighlightsOnSuccess(t *testing.T) {
	summary := reducer.Summary{
		Success:         true,
		BuildStatusLine: "BUILD SUCCESSFUL in 1s",
		ImportantLines: []string{
			"BUILD SUCCESSFUL in 1s",
			"AgentPreview report written to: /tmp/project/build/agentPreviewReports/capture-report.json",
		},
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render success output with highlights: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"BUILD SUCCESSFUL in 1s",
		"Highlights:",
		"AgentPreview report written to: /tmp/project/build/agentPreviewReports/capture-report.json",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected success highlights to contain %q, got %q", expected, rendered)
		}
	}
}

func TestRenderHumanKeepsFailureDetails(t *testing.T) {
	summary := reducer.Summary{
		Success:         false,
		BuildStatusLine: "BUILD FAILED in 245ms",
		CommandLine:     "gradle smokeSymbolCompile",
		RawLogPath:      "/tmp/raw.log",
		FailedTasks:     []string{":smokeSymbolCompile"},
		ImportantLines: []string{
			"BUILD FAILED in 245ms",
			"Execution failed for task ':smokeSymbolCompile'.",
		},
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render failure output: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"BUILD FAILED in 245ms",
		"Command: gradle smokeSymbolCompile",
		"Failed tasks:",
		"Execution failed for task ':smokeSymbolCompile'.",
		"Raw log: /tmp/raw.log",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected failure output to contain %q, got %q", expected, rendered)
		}
	}

	if strings.Count(rendered, "BUILD FAILED in 245ms") != 1 {
		t.Fatalf("expected build status line once, got %q", rendered)
	}
}

func TestRenderHumanShowsBuildScanURLOnSuccess(t *testing.T) {
	summary := reducer.Summary{
		Success:         true,
		BuildStatusLine: "BUILD SUCCESSFUL in 5s",
		BuildScanURLs:   []string{"https://develocity.internal.example/s/abc123"},
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render success output with build scan: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"BUILD SUCCESSFUL in 5s",
		"Build scan:",
		"https://develocity.internal.example/s/abc123",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected success output to contain %q, got %q", expected, rendered)
		}
	}
}

func TestRenderHumanShowsCustomMatchesOnSuccess(t *testing.T) {
	summary := reducer.Summary{
		Success:         true,
		BuildStatusLine: "BUILD SUCCESSFUL in 5s",
		CustomMatches: []reducer.CustomMatchResult{
			{
				Name:    "Firebase Test Lab",
				Matches: []string{"https://console.firebase.google.com/project/sample/testlab/histories/bh.123"},
			},
			{
				Name:    "emulator.wtf",
				Matches: []string{"https://app.emulator.wtf/runs/abc123"},
			},
		},
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render success output with custom matches: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"BUILD SUCCESSFUL in 5s",
		"Custom matches:",
		"Firebase Test Lab:",
		"https://console.firebase.google.com/project/sample/testlab/histories/bh.123",
		"emulator.wtf:",
		"https://app.emulator.wtf/runs/abc123",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected success output to contain %q, got %q", expected, rendered)
		}
	}
}

func TestRenderHumanShowsConfigCacheStatusAsPlainLine(t *testing.T) {
	for _, tc := range []struct {
		status   string
		expected string
	}{
		{"reused", "Configuration cache entry reused."},
		{"stored", "Configuration cache entry stored."},
	} {
		t.Run(tc.status, func(t *testing.T) {
			summary := reducer.Summary{
				Success:           true,
				BuildStatusLine:   "BUILD SUCCESSFUL in 3s",
				ConfigCacheStatus: tc.status,
			}

			var out bytes.Buffer
			if err := RenderHuman(&out, summary); err != nil {
				t.Fatalf("render: %v", err)
			}

			rendered := out.String()
			if !strings.Contains(rendered, tc.expected) {
				t.Fatalf("expected output to contain %q, got %q", tc.expected, rendered)
			}
			if strings.Contains(rendered, "Configuration cache:") {
				t.Fatalf("expected no section header, got %q", rendered)
			}
		})
	}
}

func TestRenderHumanShowsConfigCacheSection(t *testing.T) {
	summary := reducer.Summary{
		Success:         true,
		BuildStatusLine: "BUILD SUCCESSFUL in 3s",
		ConfigCacheProblems: []string{
			"2 problems were found storing the configuration cache.",
			"Script 'build.gradle': line 12: external process started 'git --version'",
		},
		ConfigCacheReportURL: "file:///tmp/build/reports/configuration-cache/abc/configuration-cache-report.html",
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"Configuration cache:",
		"2 problems were found storing the configuration cache.",
		"Script 'build.gradle': line 12: external process started 'git --version'",
		"Report: file:///tmp/build/reports/configuration-cache/abc/configuration-cache-report.html",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, rendered)
		}
	}
}

func TestRenderHumanShowsEnrichmentScanMetadataAndWarnings(t *testing.T) {
	summary := reducer.Summary{
		Success:         true,
		BuildStatusLine: "BUILD SUCCESSFUL in 5s",
		WarningCount:    1,
		Warnings:        []string{"JUnit report scan incomplete: discovered 2, parsed 0, skipped 2"},
		JUnitScan: &reducer.JUnitScanMetadata{
			Discovered: 2,
			Skipped:    2,
			Errors:     []string{"TEST-bad.xml: XML syntax error"},
		},
		ArtifactScan: &reducer.ArtifactScanMetadata{
			Discovered: 21,
			Reported:   20,
			Skipped:    1,
			Truncated:  true,
		},
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render enrichment metadata: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"Warnings: 1",
		"JUnit report scan incomplete: discovered 2, parsed 0, skipped 2",
		"JUnit reports: 2 discovered, 0 parsed, 2 skipped",
		"JUnit report scan errors:",
		"TEST-bad.xml: XML syntax error",
		"Artifacts scan: 21 discovered, 20 reported, 1 skipped",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected enrichment output to contain %q, got %q", expected, rendered)
		}
	}
}

func TestRenderHumanShowsArtifactsAndOmittedCompilationOutputs(t *testing.T) {
	summary := reducer.Summary{
		Success:         true,
		BuildStatusLine: "BUILD SUCCESSFUL in 5s",
		PassedTestCount: 24,
		FailedTestCount: 1,
		Artifacts: []reducer.Artifact{
			{Kind: "APK", Path: "androidApp/build/outputs/apk/debug/androidApp-debug.apk", SizeBytes: 24 * 1024 * 1024},
			{Kind: "JAR", Path: "server/build/libs/server.jar", SizeBytes: 512 * 1024},
		},
		GeneratedClassFileCount:   381,
		GeneratedCodegenFileCount: 24,
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render success output with artifacts: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"BUILD SUCCESSFUL in 5s",
		"Tests: 24 passed, 1 failed",
		"Artifacts:",
		"APK: androidApp/build/outputs/apk/debug/androidApp-debug.apk",
		"JAR: server/build/libs/server.jar",
		"Compilation outputs omitted:",
		"381 .class files generated.",
		"24 generated source/codegen files updated.",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected success output to contain %q, got %q", expected, rendered)
		}
	}
}

func TestRenderHumanShowsDiagnosisInsteadOfHighlights(t *testing.T) {
	summary := reducer.Summary{
		Success:         false,
		BuildStatusLine: "BUILD FAILED in 14s",
		CommandLine:     "gradle compileDebugKotlin",
		RawLogPath:      "/tmp/raw.log",
		FailedTasks:     []string{":app:compileDebugKotlin"},
		ImportantLines: []string{
			"Execution failed for task ':app:compileDebugKotlin'.",
			"e: Daemon compilation failed: Could not connect to Kotlin compile daemon",
		},
		Diagnostics: []reducer.Diagnostic{{
			ID:         "kotlin_daemon_failure",
			Category:   "kotlin",
			Severity:   "error",
			Summary:    "Kotlin compiler daemon failure",
			Evidence:   []string{"Could not connect to Kotlin compile daemon", "Daemon compilation failed"},
			NextChecks: []string{"Inspect Gradle/Kotlin JVM args", "Retry with --no-daemon only if daemon state looks corrupted"},
			Confidence: "high",
		}},
	}

	var out bytes.Buffer
	if err := RenderHuman(&out, summary); err != nil {
		t.Fatalf("render failure output: %v", err)
	}

	rendered := out.String()
	for _, expected := range []string{
		"BUILD FAILED in 14s",
		"Diagnosis: Kotlin compiler daemon failure",
		"Evidence:",
		"Could not connect to Kotlin compile daemon",
		"Next checks:",
		"Inspect Gradle/Kotlin JVM args",
		"Failed tasks:",
		":app:compileDebugKotlin",
		"Raw log: /tmp/raw.log",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected diagnostic output to contain %q, got %q", expected, rendered)
		}
	}
	if strings.Contains(rendered, "Highlights:") {
		t.Fatalf("expected diagnostics to replace highlights, got %q", rendered)
	}
}
