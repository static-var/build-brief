package reducer

import "strings"

const maxDiagnosticEvidence = 3

type diagnosticRule struct {
	ID                    string
	Category              string
	Severity              string
	Summary               string
	Confidence            string
	Priority              int
	Needles               []string
	NextChecks            []string
	RequiresTestContext   bool
	RequiresKotlinContext bool
}

type diagnosticEvidence map[string][]string

func newDiagnosticEvidence() diagnosticEvidence {
	return make(diagnosticEvidence)
}

func (e diagnosticEvidence) collect(line string) {
	if isDependencyCoordinateNotFound(line) {
		e.add("dependency_resolution_failure", line)
	}
	for _, rule := range diagnosticRules {
		if matchesAny(line, rule.Needles) {
			e.add(rule.ID, line)
		}
	}
}

func (e diagnosticEvidence) add(id, line string) {
	clean := cleanEvidence(line)
	if containsString(e[id], clean) || len(e[id]) >= maxDiagnosticEvidence {
		return
	}
	e[id] = append(e[id], strings.Clone(clean))
}

var diagnosticRules = []diagnosticRule{
	{
		ID:         "android_sdk_license",
		Category:   "android_sdk",
		Severity:   "error",
		Summary:    "Android SDK license failure",
		Confidence: "high",
		Priority:   10,
		Needles:    []string{"licences have not been accepted", "licenses have not been accepted", "failed to install the following android sdk packages"},
		NextChecks: []string{"Accept Android SDK licenses for the configured SDK", "Verify ANDROID_HOME or sdk.dir points at the SDK used by Gradle"},
	},
	{
		ID:         "android_sdk_missing",
		Category:   "android_sdk",
		Severity:   "error",
		Summary:    "Android SDK location missing",
		Confidence: "high",
		Priority:   11,
		Needles:    []string{"sdk location not found", "android sdk location not found", "failed to find target with hash string", "sdk directory", "android_home"},
		NextChecks: []string{"Verify ANDROID_HOME or local.properties sdk.dir", "Install the required Android SDK platform/build-tools"},
	},
	{
		ID:         "dependency_resolution_failure",
		Category:   "dependency_resolution",
		Severity:   "error",
		Summary:    "Dependency resolution failure",
		Confidence: "high",
		Priority:   20,
		Needles:    []string{"could not resolve all files for configuration", "could not resolve all dependencies", "could not find dependency", "could not GET", "could not HEAD", "no matching variant of"},
		NextChecks: []string{"Check dependency coordinates and versions", "Check repositories, credentials, and network/proxy access"},
	},
	{
		ID:         "kotlin_daemon_failure",
		Category:   "kotlin",
		Severity:   "error",
		Summary:    "Kotlin compiler daemon failure",
		Confidence: "high",
		Priority:   30,
		Needles:    []string{"could not connect to kotlin compile daemon", "daemon compilation failed", "kotlin compile daemon"},
		NextChecks: []string{"Inspect Gradle/Kotlin JVM args", "Retry with --no-daemon only if daemon state looks corrupted"},
	},
	{
		ID:                    "kotlin_oom",
		Category:              "kotlin",
		Severity:              "error",
		Summary:               "Kotlin compiler out of memory",
		Confidence:            "high",
		Priority:              31,
		Needles:               []string{"java.lang.outofmemoryerror", "kotlin daemon is out of memory", "gc overhead limit exceeded"},
		NextChecks:            []string{"Inspect org.gradle.jvmargs and kotlin daemon JVM args", "Check recent source/generated-code growth before increasing heap"},
		RequiresKotlinContext: true,
	},
	{
		ID:         "android_gradle_plugin_error",
		Category:   "android_gradle_plugin",
		Severity:   "error",
		Summary:    "Android Gradle Plugin error",
		Confidence: "medium",
		Priority:   40,
		Needles:    []string{"android gradle plugin", "com.android.tools.build:gradle", "requires a newer compileSdk", "minimum supported gradle version"},
		NextChecks: []string{"Check AGP, Gradle, JDK, and compileSdk compatibility", "Inspect plugin versions in build.gradle/settings.gradle"},
	},
	{
		ID:         "configuration_cache_failure",
		Category:   "configuration_cache",
		Severity:   "error",
		Summary:    "Configuration cache failure",
		Confidence: "medium",
		Priority:   45,
		Needles:    []string{"configuration cache problems found", "configuration cache problems", "problems were found storing the configuration cache", "cannot serialize object of type", "configuration cache state could not be cached"},
		NextChecks: []string{"Open the configuration cache report if present", "Inspect tasks/plugins that access unsupported Gradle APIs at configuration time"},
	},
	{
		ID:         "lint_failure",
		Category:   "lint",
		Severity:   "error",
		Summary:    "Lint failure",
		Confidence: "medium",
		Priority:   50,
		Needles:    []string{"lint found fatal errors", "lint found errors", "abort on error", "lint-results", "lintVital"},
		NextChecks: []string{"Open the lint report", "Fix or intentionally baseline the reported lint issues"},
	},
	{
		ID:                  "flaky_test_failure",
		Category:            "test",
		Severity:            "error",
		Summary:             "Potential flaky test failure",
		Confidence:          "low",
		Priority:            60,
		Needles:             []string{"timed out", "timeout", "connection reset", "refused", "flaky", "race condition"},
		NextChecks:          []string{"Inspect test logs and timing-sensitive setup", "Rerun the failed test to confirm whether the failure is reproducible"},
		RequiresTestContext: true,
	},
}

func Diagnose(evidence diagnosticEvidence, summary Summary) []Diagnostic {
	if summary.Success {
		return nil
	}
	diagnostics := make([]Diagnostic, 0)
	for _, rule := range diagnosticRules {
		if rule.RequiresTestContext && !hasTestContext(summary) {
			continue
		}
		if rule.RequiresKotlinContext && !hasKotlinContext(summary, evidence) {
			continue
		}
		ruleEvidence := append([]string{}, evidence[rule.ID]...)
		if len(ruleEvidence) == 0 {
			ruleEvidence = matchingEvidence(summary.ImportantLines, rule.Needles)
		}
		if len(ruleEvidence) == 0 {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			ID:         rule.ID,
			Category:   rule.Category,
			Severity:   rule.Severity,
			Summary:    rule.Summary,
			Evidence:   ruleEvidence,
			NextChecks: append([]string{}, rule.NextChecks...),
			Confidence: rule.Confidence,
		})
	}
	if len(diagnostics) == 0 && len(summary.FailedTests) > 0 {
		diagnostics = append(diagnostics, Diagnostic{
			ID:         "test_failure",
			Category:   "test",
			Severity:   "error",
			Summary:    "Test failure",
			Evidence:   genericTestEvidence(summary),
			NextChecks: []string{"Inspect the failing test report", "Use the test name to rerun the smallest failing test scope"},
			Confidence: "medium",
		})
	}
	return diagnostics
}

func genericTestEvidence(summary Summary) []string {
	evidence := make([]string, 0, maxDiagnosticEvidence)
	for _, line := range summary.ImportantLines {
		trimmed := cleanEvidence(line)
		if trimmed == "" || isGenericTestEvidenceNoise(trimmed) || containsString(evidence, trimmed) {
			continue
		}
		evidence = append(evidence, trimmed)
		if len(evidence) >= maxDiagnosticEvidence {
			return evidence
		}
	}
	if len(evidence) > 0 {
		return evidence
	}
	return firstN(summary.FailedTests, maxDiagnosticEvidence)
}

func isGenericTestEvidenceNoise(line string) bool {
	return strings.HasPrefix(line, "BUILD FAILED") ||
		strings.HasPrefix(line, "FAILURE:") ||
		strings.HasPrefix(line, "* What went wrong:") ||
		strings.HasPrefix(line, "Execution failed for task ") ||
		strings.HasSuffix(line, " FAILED")
}

func hasTestContext(summary Summary) bool {
	if len(summary.FailedTests) > 0 {
		return true
	}
	for _, task := range summary.FailedTasks {
		if strings.Contains(strings.ToLower(task), "test") {
			return true
		}
	}
	return false
}

func hasKotlinContext(summary Summary, evidence diagnosticEvidence) bool {
	for _, task := range summary.FailedTasks {
		if strings.Contains(strings.ToLower(task), "kotlin") {
			return true
		}
	}
	for _, line := range append(append([]string{}, summary.ImportantLines...), evidence["kotlin_oom"]...) {
		if strings.Contains(strings.ToLower(line), "kotlin") {
			return true
		}
	}
	return false
}

func matchingEvidence(lines []string, needles []string) []string {
	seen := map[string]struct{}{}
	evidence := make([]string, 0, maxDiagnosticEvidence)
	for _, line := range lines {
		lower := strings.ToLower(line)
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(needle)) {
				clean := cleanEvidence(line)
				if _, ok := seen[clean]; ok {
					break
				}
				seen[clean] = struct{}{}
				evidence = append(evidence, clean)
				if len(evidence) >= maxDiagnosticEvidence {
					return evidence
				}
				break
			}
		}
	}
	return evidence
}

func isDependencyCoordinateNotFound(line string) bool {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "could not find ") || strings.Contains(lower, "could not find method ") {
		return false
	}
	tail := strings.TrimSpace(line[strings.Index(lower, "could not find ")+len("could not find "):])
	return strings.Count(tail, ":") >= 2
}

func matchesAny(line string, needles []string) bool {
	lower := strings.ToLower(line)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func cleanEvidence(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "> ")
	line = strings.TrimPrefix(line, "e: ")
	line = strings.TrimRight(line, ".")
	return line
}

func firstN(items []string, n int) []string {
	if len(items) <= n {
		return append([]string{}, items...)
	}
	return append([]string{}, items[:n]...)
}
