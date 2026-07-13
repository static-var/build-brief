package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"build-brief/internal/config"
	"build-brief/internal/gradle"
)

type Status string

const (
	StatusPass Status = "PASS"
	StatusInfo Status = "INFO"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
)

type Options struct {
	ProjectDir     string
	GradlePath     string
	LogDir         string
	GradleUserHome string
	ConfigPath     string
	Mode           string
	Version        string
}

type Report struct {
	Options Options
	Results []Result
}

type Result struct {
	Group   string
	Name    string
	Status  Status
	Summary string
	Detail  []string
}

func Check(opts Options) Report {
	report := Report{Options: opts}
	projectDir := strings.TrimSpace(opts.ProjectDir)
	if projectDir == "" {
		projectDir = "."
	}

	projectOK := false
	if abs, err := filepath.Abs(projectDir); err == nil {
		projectDir = abs
	}
	if info, err := os.Stat(projectDir); err != nil {
		report.add("Project", "project directory", StatusFail, "not available", err.Error())
	} else if !info.IsDir() {
		report.add("Project", "project directory", StatusFail, "not a directory", projectDir)
	} else {
		projectOK = true
		report.add("Project", "project directory", StatusPass, projectDir)
	}

	checkGradleMarkers(&report, projectDir, projectOK)
	checkConfig(&report, projectDir, opts.ConfigPath, projectOK)
	checkMode(&report, opts.Mode)
	checkGradle(&report, projectDir, opts.GradlePath, projectOK)
	checkDirOption(&report, "Environment", "log directory", opts.LogDir)
	checkDirOption(&report, "Environment", "Gradle user home", opts.GradleUserHome)
	checkInstall(&report, opts.Version)

	return report
}

// Run is kept as a small compatibility wrapper for tests/callers.
func Run(opts Options) Report { return Check(opts) }

func (r Report) HasFailures() bool {
	for _, result := range r.Results {
		if result.Status == StatusFail {
			return true
		}
	}
	return false
}

func (r *Report) add(group, name string, status Status, summary string, detail ...string) {
	r.Results = append(r.Results, Result{Group: group, Name: name, Status: status, Summary: summary, Detail: nonEmpty(detail)})
}

func checkGradleMarkers(report *Report, projectDir string, projectOK bool) {
	if !projectOK {
		report.add("Project", "Gradle markers", StatusWarn, "skipped because project directory is unavailable")
		return
	}
	markers := []string{"settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts"}
	found := make([]string, 0, len(markers))
	for _, marker := range markers {
		if _, err := os.Stat(filepath.Join(projectDir, marker)); err == nil {
			found = append(found, marker)
		}
	}
	if len(found) > 0 {
		report.add("Project", "Gradle markers", StatusPass, strings.Join(found, ", "))
		return
	}
	report.add("Project", "Gradle markers", StatusWarn, "none found", strings.Join(markers, ", "))
}

func checkConfig(report *Report, projectDir, configPath string, projectOK bool) {
	if !projectOK {
		report.add("Config", "config", StatusWarn, "skipped because project directory is unavailable")
		return
	}
	cfg, loadedPath, err := config.Load(projectDir, configPath)
	if err != nil {
		report.add("Config", "config", StatusFail, "validation failed", err.Error())
		return
	}
	if loadedPath == "" {
		report.add("Config", "default config", StatusPass, "absent; built-in defaults will be used")
		return
	}
	ruleCount := len(cfg.Matches)
	report.add("Config", "config", StatusPass, loadedPath, fmt.Sprintf("custom matches: %d", ruleCount))
	if ruleCount > config.CustomMatchRuleLimit {
		report.add("Config", "custom match limits", StatusWarn, fmt.Sprintf("%d configured; %d retained; %d ignored", ruleCount, config.CustomMatchRuleLimit, ruleCount-config.CustomMatchRuleLimit))
	}
}

func checkMode(report *Report, mode string) {
	trimmed := strings.TrimSpace(mode)
	if trimmed == "" {
		report.add("Environment", "mode", StatusInfo, "not set; human output will be used")
		return
	}
	switch strings.ToLower(trimmed) {
	case "human", "raw":
		report.add("Environment", "mode", StatusPass, trimmed)
	case "json":
		report.add("Environment", "mode", StatusPass, "human")
	default:
		report.add("Environment", "mode", StatusFail, "invalid mode", trimmed)
	}
}

func checkGradle(report *Report, projectDir, gradlePath string, projectOK bool) {
	if !projectOK {
		report.add("Gradle", "resolution", StatusWarn, "skipped because project directory is unavailable")
		report.add("Gradle", "wrapper health", StatusWarn, "skipped because project directory is unavailable")
		return
	}
	cmd, err := gradle.Resolve(projectDir, gradlePath, "")
	if err != nil {
		report.add("Gradle", "resolution", StatusFail, "could not resolve Gradle without executing it", err.Error())
	} else if err := validateExecutablePath(cmd.Executable); err != nil {
		report.add("Gradle", "resolution", StatusFail, "resolved Gradle path is not executable", err.Error())
	} else {
		report.add("Gradle", "resolution", StatusPass, cmd.Executable, "source: "+string(cmd.Source))
	}
	checkWrapperHealth(report, projectDir)
}

func validateExecutablePath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

func checkWrapperHealth(report *Report, projectDir string) {
	wrapper := filepath.Join(projectDir, "gradlew")
	if runtime.GOOS == "windows" {
		wrapper = filepath.Join(projectDir, "gradlew.bat")
	}
	info, err := os.Stat(wrapper)
	if err != nil {
		report.add("Gradle", "wrapper health", StatusWarn, "wrapper script is absent", wrapper)
		return
	}
	if info.IsDir() {
		report.add("Gradle", "wrapper health", StatusFail, "wrapper path is a directory", wrapper)
		return
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		report.add("Gradle", "wrapper health", StatusFail, "wrapper is not executable", wrapper, "missing executable bit")
		return
	}
	detail := []string{wrapper}
	status := StatusPass
	summary := "wrapper looks healthy"
	for _, path := range []string{filepath.Join(projectDir, "gradle", "wrapper", "gradle-wrapper.properties"), filepath.Join(projectDir, "gradle", "wrapper", "gradle-wrapper.jar")} {
		if _, err := os.Stat(path); err != nil {
			status = StatusWarn
			summary = "wrapper script exists but wrapper files are missing"
			detail = append(detail, "missing: "+path)
		}
	}
	report.add("Gradle", "wrapper health", status, summary, detail...)
}

func checkDirOption(report *Report, group, name, path string) {
	if strings.TrimSpace(path) == "" {
		report.add(group, name, StatusInfo, "not configured")
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		report.add(group, name, StatusWarn, "does not exist yet", err.Error())
		return
	}
	if !info.IsDir() {
		report.add(group, name, StatusFail, "is not a directory", path)
		return
	}
	report.add(group, name, StatusPass, path)
}

func checkInstall(report *Report, version string) {
	exe, err := os.Executable()
	if err != nil {
		report.add("Install", "current executable", StatusWarn, "could not determine current executable", err.Error(), "version: "+version)
		return
	}
	report.add("Install", "current executable", StatusPass, exe)
	report.add("Install", "version", StatusPass, version)
	pathExe, lookErr := exec.LookPath("build-brief")
	if lookErr != nil {
		report.add("Install", "PATH build-brief", StatusWarn, "not found", lookErr.Error())
		return
	}
	if samePath(exe, pathExe) {
		report.add("Install", "PATH build-brief", StatusPass, pathExe)
		return
	}
	report.add("Install", "PATH build-brief", StatusWarn, "differs from current executable", "current: "+exe, "PATH: "+pathExe)
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return absA == absB
	}
	return a == b
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func statusRank(status Status) int {
	switch status {
	case StatusFail:
		return 4
	case StatusWarn:
		return 3
	case StatusPass:
		return 2
	default:
		return 1
	}
}

func SummaryStatus(report Report) Status {
	status := StatusInfo
	for _, result := range report.Results {
		if statusRank(result.Status) > statusRank(status) {
			status = result.Status
		}
	}
	return status
}
