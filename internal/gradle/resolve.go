package gradle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

type Source string

const (
	SourceExplicit Source = "explicit"
	SourceWrapper  Source = "wrapper"
	SourceSystem   Source = "system"

	trackingLineVersion   = "v2:"
	redactedLegacyCommand = "<redacted legacy command>"
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
	stable := make([]string, 0, 3+len(args))
	sanitized := sanitizeGradleArgs(args)

	stable = append(stable, "--console=plain")

	if opts.GradleUserHome != "" && !hasGradleUserHomeFlag(sanitized) {
		stable = append(stable, "--gradle-user-home", opts.GradleUserHome)
	}

	return append(stable, sanitized...)
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
		case arg == "--console":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for Gradle flag %s", arg)
			}
			i++
		case strings.HasPrefix(arg, "--console="):
			continue
		case arg == "--warning-mode":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for Gradle flag %s", arg)
			}
			i++
		case strings.HasPrefix(arg, "--warning-mode="):
			continue
		case arg == "--gradle-user-home":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for Gradle flag %s", arg)
			}
			i++
		}
	}

	return nil
}

// SanitizeArgs redacts sensitive Gradle property values without changing execution args.
func SanitizeArgs(args []string) []string {
	sanitized := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-P" || arg == "-D" || arg == "--project-prop" || arg == "--system-prop":
			sanitized = append(sanitized, arg)
			if i+1 < len(args) {
				sanitized = append(sanitized, "<redacted>")
				i++
			}
		case strings.HasPrefix(arg, "-P") && arg != "-P":
			sanitized = append(sanitized, "-P<redacted>")
		case strings.HasPrefix(arg, "-D") && arg != "-D":
			sanitized = append(sanitized, "-D<redacted>")
		case strings.HasPrefix(arg, "--project-prop="):
			sanitized = append(sanitized, "--project-prop=<redacted>")
		case strings.HasPrefix(arg, "--system-prop="):
			sanitized = append(sanitized, "--system-prop=<redacted>")
		default:
			sanitized = append(sanitized, arg)
		}
	}
	return sanitized
}

// SanitizeCommandLine handles a shell-like command string; execution keeps structured args.
func SanitizeCommandLine(command string) string {
	return strings.Join(SanitizeArgs(parseCommandLine(command)), " ")
}

// SanitizeTrackingLine stores new labels in a distinguishable format and replaces
// ambiguous unversioned labels instead of trying to recover flattened secrets.
func SanitizeTrackingLine(command string) string {
	if strings.HasPrefix(command, trackingLineVersion) {
		return trackingLineVersion + SanitizeCommandLine(strings.TrimPrefix(command, trackingLineVersion))
	}
	if containsSensitivePropertyFlag(command) && !containsOnlyKnownSafeSensitiveProperties(command) {
		return trackingLineVersion + redactedLegacyCommand
	}
	return trackingLineVersion + SanitizeCommandLine(command)
}

// SanitizeHistoricCommand returns a display-safe label from either format.
func SanitizeHistoricCommand(command string) string {
	if strings.HasPrefix(command, trackingLineVersion) {
		return SanitizeCommandLine(strings.TrimPrefix(command, trackingLineVersion))
	}
	if containsSensitivePropertyFlag(command) && !containsOnlyKnownSafeSensitiveProperties(command) {
		return redactedLegacyCommand
	}
	return SanitizeCommandLine(command)
}

func containsOnlyKnownSafeSensitiveProperties(command string) bool {
	foundSensitiveProperty := false
	args := parseCommandLine(command)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-P" || arg == "-D" || arg == "--project-prop" || arg == "--system-prop":
			foundSensitiveProperty = true
			if i+1 >= len(args) || args[i+1] != "<redacted>" {
				return false
			}
			i++
		case arg == "-P<redacted>" || arg == "-D<redacted>" ||
			arg == "--project-prop=<redacted>" || arg == "--system-prop=<redacted>":
			foundSensitiveProperty = true
		case strings.HasPrefix(arg, "-P") || strings.HasPrefix(arg, "-D") ||
			strings.HasPrefix(arg, "--project-prop=") || strings.HasPrefix(arg, "--system-prop="):
			return false
		}
	}
	return foundSensitiveProperty
}

func containsSensitivePropertyFlag(command string) bool {
	for _, arg := range parseCommandLine(command) {
		switch {
		case arg == "-P", arg == "-D", arg == "--project-prop", arg == "--system-prop":
			return true
		case strings.HasPrefix(arg, "-P"), strings.HasPrefix(arg, "-D"),
			strings.HasPrefix(arg, "--project-prop="), strings.HasPrefix(arg, "--system-prop="):
			return true
		}
	}
	return false
}

// parseCommandLine only groups shell-style quotes and escaped whitespace; it does not execute a shell.
func parseCommandLine(command string) []string {
	runes := []rune(command)
	args := make([]string, 0)
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	started := false

	flush := func() {
		if !started {
			return
		}
		args = append(args, current.String())
		current.Reset()
		started = false
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && !inSingleQuote:
			if i+1 < len(runes) {
				next := runes[i+1]
				if unicode.IsSpace(next) || next == '\\' || next == '\'' || next == '"' {
					current.WriteRune(next)
					started = true
					i++
					continue
				}
			}
			current.WriteRune(r)
			started = true
		case r == '\'' && !inDoubleQuote:
			inSingleQuote = !inSingleQuote
			started = true
		case r == '"' && !inSingleQuote:
			inDoubleQuote = !inDoubleQuote
			started = true
		case unicode.IsSpace(r) && !inSingleQuote && !inDoubleQuote:
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	flush()
	return args
}

func (c Command) DisplayLine() string {
	return strings.Join(append([]string{filepath.Base(c.Executable)}, SanitizeArgs(c.Args)...), " ")
}

func (c Command) TrackingLine() string {
	sanitizedArgs := SanitizeArgs(c.Args)
	filtered := make([]string, 0, len(sanitizedArgs))
	skipNext := false
	nextValueMode := ""
	for _, arg := range sanitizedArgs {
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
		case looksLikeGradleInvocation(arg):
			continue
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
		case arg == "-P" || arg == "-D":
			filtered = append(filtered, arg)
			skipNext = true
			nextValueMode = "redact"
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
		case strings.HasPrefix(arg, "-P"), strings.HasPrefix(arg, "-D"),
			strings.HasPrefix(arg, "--project-prop="), strings.HasPrefix(arg, "--system-prop="):
			filtered = append(filtered, arg)
		case shouldKeepTrackingArg(arg):
			filtered = append(filtered, arg)
		default:
			continue
		}
	}

	return trackingLineVersion + normalizeTrackingCommand(strings.Join(append([]string{filepath.Base(c.Executable)}, filtered...), " "))
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

func hasGradleUserHomeFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--gradle-user-home" || strings.HasPrefix(arg, "--gradle-user-home=") {
			return true
		}
	}
	return false
}

func sanitizeGradleArgs(args []string) []string {
	sanitized := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--console":
			i++
			continue
		case strings.HasPrefix(arg, "--console="):
			continue
		case arg == "--warning-mode":
			i++
			continue
		case strings.HasPrefix(arg, "--warning-mode="):
			continue
		case arg == "--daemon" || arg == "--no-daemon":
			continue
		case isOutputShapingFlag(arg):
			continue
		default:
			sanitized = append(sanitized, arg)
		}
	}
	return sanitized
}

func isOutputShapingFlag(arg string) bool {
	switch arg {
	case "--quiet", "-q", "--warn", "-w", "--silent", "--silence", "--slience", "--plain-text", "--plain":
		return true
	default:
		return false
	}
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

func normalizeTrackingCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}

	normalized := make([]string, 0, len(fields))
	normalized = append(normalized, fields[0])
	for _, field := range fields[1:] {
		if looksLikeGradleInvocation(field) {
			continue
		}
		normalized = append(normalized, field)
	}
	return strings.Join(normalized, " ")
}
