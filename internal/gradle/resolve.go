package gradle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Source string

const (
	SourceExplicit Source = "explicit"
	SourceWrapper  Source = "wrapper"
	SourceSystem   Source = "system"
)

type Command struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	ProjectDir string   `json:"project_dir"`
	Source     Source   `json:"source"`
}

type StableArgsOptions struct {
	GradleUserHome string
}

func Resolve(projectDir, explicitPath, invocation string) (Command, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return Command{}, fmt.Errorf("resolve project directory: %w", err)
	}

	if explicitPath != "" {
		return resolveExplicitExecutable(absProjectDir, explicitPath)
	}

	if invocation != "" {
		return resolveInvocation(absProjectDir, invocation)
	}

	for _, candidate := range wrapperCandidates(absProjectDir) {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}

		return Command{
			Executable: candidate,
			ProjectDir: absProjectDir,
			Source:     SourceWrapper,
		}, nil
	}

	systemGradle, err := exec.LookPath("gradle")
	if err != nil {
		return Command{}, fmt.Errorf("could not find gradlew in %s or system gradle on PATH", absProjectDir)
	}

	return Command{
		Executable: systemGradle,
		ProjectDir: absProjectDir,
		Source:     SourceSystem,
	}, nil
}

func ApplyStableArgs(args []string, opts StableArgsOptions) []string {
	stable := make([]string, 0, 4+len(args))

	if !hasConsoleFlag(args) {
		stable = append(stable, "--console=plain")
	}

	if opts.GradleUserHome != "" && !hasGradleUserHomeFlag(args) {
		stable = append(stable, "--gradle-user-home", opts.GradleUserHome)
	}

	if !hasDaemonFlag(args) {
		stable = append(stable, "--no-daemon")
	}

	return append(stable, args...)
}

func SplitInvocation(args []string) (string, []string) {
	if len(args) == 0 {
		return "", args
	}

	if !looksLikeGradleInvocation(args[0]) {
		return "", args
	}

	return args[0], args[1:]
}

func ValidateArgs(args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--daemon":
			return fmt.Errorf("Gradle flag %q is not allowed; build-brief always runs Gradle with --no-daemon", arg)
		case arg == "--quiet" || arg == "-q" || arg == "--warn" || arg == "-w" || arg == "--silent" || arg == "--silence" || arg == "--slience":
			return fmt.Errorf("Gradle flag %q is not allowed; build-brief already reduces noisy output and needs the normal Gradle signal", arg)
		case arg == "--console":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for Gradle flag %s", arg)
			}
			i++
			value := strings.TrimSpace(args[i])
			if !strings.EqualFold(value, "plain") {
				return fmt.Errorf("Gradle flag %q with value %q is not allowed; build-brief always uses --console=plain", arg, value)
			}
		case strings.HasPrefix(arg, "--console="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--console="))
			if !strings.EqualFold(value, "plain") {
				return fmt.Errorf("Gradle flag %q with value %q is not allowed; build-brief always uses --console=plain", "--console", value)
			}
		case arg == "--warning-mode":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for Gradle flag %s", arg)
			}
			i++
			value := strings.TrimSpace(args[i])
			if strings.EqualFold(value, "none") {
				return fmt.Errorf("Gradle flag %q with value %q is not allowed; build-brief keeps warnings visible in its summary", arg, value)
			}
		case strings.HasPrefix(arg, "--warning-mode="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--warning-mode="))
			if strings.EqualFold(value, "none") {
				return fmt.Errorf("Gradle flag %q with value %q is not allowed; build-brief keeps warnings visible in its summary", "--warning-mode", value)
			}
		}
	}

	return nil
}

func (c Command) DisplayLine() string {
	return strings.Join(append([]string{filepath.Base(c.Executable)}, c.Args...), " ")
}

func (c Command) TrackingLine() string {
	filtered := make([]string, 0, len(c.Args))
	skipNext := false
	nextValueMode := ""
	for _, arg := range c.Args {
		if skipNext {
			skipNext = false
			switch nextValueMode {
			case "keep":
				filtered = append(filtered, arg)
			case "redact":
				filtered = append(filtered, "<redacted>")
			}
			nextValueMode = ""
			continue
		}

		switch {
		case arg == "--console" || strings.HasPrefix(arg, "--console="):
			continue
		case arg == "--daemon" || arg == "--no-daemon":
			continue
		case arg == "--gradle-user-home":
			skipNext = true
			nextValueMode = "drop"
			continue
		case strings.HasPrefix(arg, "--gradle-user-home="):
			continue
		case arg == "--project-prop" || arg == "--system-prop":
			filtered = append(filtered, arg)
			skipNext = true
			nextValueMode = "redact"
		case arg == "--tests" || arg == "-x" || arg == "--exclude-task":
			filtered = append(filtered, arg)
			skipNext = true
			nextValueMode = "keep"
		case strings.HasPrefix(arg, "--tests="), strings.HasPrefix(arg, "--exclude-task="):
			filtered = append(filtered, arg)
		case shouldRedactTrackingArg(arg):
			filtered = append(filtered, redactTrackingArg(arg))
		case shouldKeepTrackingArg(arg):
			filtered = append(filtered, arg)
		default:
			continue
		}
	}

	return strings.Join(append([]string{filepath.Base(c.Executable)}, filtered...), " ")
}

func shouldRedactTrackingArg(arg string) bool {
	return strings.HasPrefix(arg, "-P") ||
		strings.HasPrefix(arg, "-D") ||
		arg == "--project-prop" ||
		arg == "--system-prop"
}

func redactTrackingArg(arg string) string {
	switch {
	case strings.HasPrefix(arg, "-P"):
		return "-P<redacted>"
	case strings.HasPrefix(arg, "-D"):
		return "-D<redacted>"
	default:
		return "<redacted>"
	}
}

func shouldKeepTrackingArg(arg string) bool {
	if arg == "" {
		return false
	}
	if !strings.HasPrefix(arg, "-") {
		return true
	}

	switch arg {
	case "--stacktrace",
		"--full-stacktrace",
		"--scan",
		"--no-scan",
		"--parallel",
		"--no-parallel",
		"--rerun-tasks",
		"--offline",
		"--refresh-dependencies",
		"--continue",
		"--dry-run",
		"--info",
		"--debug",
		"-i",
		"-d",
		"-s":
		return true
	default:
		return false
	}
}

func wrapperCandidates(projectDir string) []string {
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(projectDir, "gradlew.bat"),
			filepath.Join(projectDir, "gradlew"),
		}
	}

	return []string{
		filepath.Join(projectDir, "gradlew"),
		filepath.Join(projectDir, "gradlew.bat"),
	}
}

func looksLikeGradleInvocation(arg string) bool {
	switch gradleBaseName(arg) {
	case "gradle", "gradlew", "gradlew.bat":
		return true
	default:
		return false
	}
}

func hasConsoleFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--console" || strings.HasPrefix(arg, "--console=") {
			return true
		}
	}
	return false
}

func hasDaemonFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--daemon" || arg == "--no-daemon" {
			return true
		}
	}
	return false
}

func hasGradleUserHomeFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--gradle-user-home" || strings.HasPrefix(arg, "--gradle-user-home=") {
			return true
		}
	}
	return false
}

func resolveInvocation(projectDir, invocation string) (Command, error) {
	if strings.ContainsAny(invocation, `/\`) {
		return resolveExecutable(projectDir, invocation, SourceWrapper)
	}

	systemGradle, err := exec.LookPath(invocation)
	if err != nil {
		return Command{}, fmt.Errorf("could not find %q on PATH", invocation)
	}

	return Command{
		Executable: systemGradle,
		ProjectDir: projectDir,
		Source:     SourceSystem,
	}, nil
}

func resolveExplicitExecutable(projectDir, executable string) (Command, error) {
	absGradle, err := filepath.Abs(filepath.FromSlash(strings.ReplaceAll(executable, `\`, `/`)))
	if err != nil {
		return Command{}, fmt.Errorf("resolve Gradle executable path: %w", err)
	}
	if filepath.IsAbs(executable) {
		absGradle = executable
	}
	if _, err := os.Stat(absGradle); err != nil {
		return Command{}, fmt.Errorf("Gradle executable %q is not available: %w", absGradle, err)
	}

	return Command{
		Executable: absGradle,
		ProjectDir: projectDir,
		Source:     SourceExplicit,
	}, nil
}

func resolveExecutable(projectDir, executable string, source Source) (Command, error) {
	absGradle, err := filepath.Abs(filepath.Join(projectDir, filepath.FromSlash(strings.ReplaceAll(executable, `\`, `/`))))
	if err != nil {
		return Command{}, fmt.Errorf("resolve Gradle executable path: %w", err)
	}

	if filepath.IsAbs(executable) {
		absGradle = executable
	}

	if _, err := os.Stat(absGradle); err != nil {
		return Command{}, fmt.Errorf("Gradle executable %q is not available: %w", absGradle, err)
	}

	return Command{
		Executable: absGradle,
		ProjectDir: projectDir,
		Source:     source,
	}, nil
}

func gradleBaseName(arg string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(arg), `\`, `/`)
	return strings.ToLower(filepath.Base(normalized))
}
