package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"build-brief/internal/gradle"
	"build-brief/internal/install"
	"build-brief/internal/output"
	"build-brief/internal/reducer"
	"build-brief/internal/rewrite"
	"build-brief/internal/runner"
	"build-brief/internal/tracking"
)

type Options struct {
	Mode           string
	ProjectDir     string
	GradlePath     string
	LogDir         string
	GradleUserHome string
	Help           bool
	Version        bool
	Install        bool
	InstallForce   bool
	Global         bool
}

var (
	currentDir = os.Getwd
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "gains", "gain":
			return runGains(args[1:], stdout, stderr)
		case "rewrite":
			return runRewrite(args[1:], stdout, stderr)
		}
	}

	opts, gradleArgs, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: %v\n\n", err)
		writeUsage(stderr)
		return 2
	}

	if opts.Help {
		writeUsage(stdout)
		return 0
	}

	if opts.Version {
		fmt.Fprintf(stdout, "build-brief %s\n", Version)
		return 0
	}

	if opts.Global {
		return runGlobalInstall(stdin, stdout, stderr)
	}

	if opts.Install || opts.InstallForce {
		return runLocalInstall(stdout, stderr, opts.InstallForce)
	}

	if len(gradleArgs) == 0 {
		fmt.Fprintln(stderr, "build-brief: missing Gradle arguments")
		fmt.Fprintln(stderr)
		writeUsage(stderr)
		return 2
	}

	if opts.ProjectDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "build-brief: resolve project directory: %v\n", err)
			return 1
		}
		opts.ProjectDir = wd
	}

	invocation, gradleArgs := gradle.SplitInvocation(gradleArgs)
	if opts.GradlePath != "" && invocation != "" {
		fmt.Fprintln(stderr, "build-brief: cannot combine --gradle with an explicit Gradle command token; use one or the other")
		return 2
	}
	if err := gradle.ValidateArgs(gradleArgs); err != nil {
		fmt.Fprintf(stderr, "build-brief: %v\n", err)
		return 2
	}

	command, err := gradle.Resolve(opts.ProjectDir, opts.GradlePath, invocation)
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: %v\n", err)
		return 1
	}
	command.Args = gradle.ApplyStableArgs(gradleArgs, gradle.StableArgsOptions{
		GradleUserHome: opts.GradleUserHome,
	})
	trackingCommand := command.TrackingLine()

	runResult, err := runner.RunWithOptions(ctx, command, opts.LogDir, runner.Options{
		ProgressInterval: 30 * time.Second,
		Progress: func(event runner.ProgressEvent) {
			fmt.Fprintf(stderr, "build-brief: still running after %s (raw log: %s)\n", formatElapsed(event.Elapsed), event.RawLogPath)
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: %v\n", err)
		if runResult.RawLogPath != "" {
			fmt.Fprintf(stderr, "Raw log: %s\n", runResult.RawLogPath)
		}
		if runResult.ExitCode > 0 {
			return runResult.ExitCode
		}
		return 1
	}

	rawTokens, err := tracking.EstimateFileTokens(runResult.RawLogPath)
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: estimate raw tokens: %v\n", err)
		return 1
	}

	switch opts.Mode {
	case "raw":
		if err := output.RenderRaw(stdout, runResult.RawLogPath); err != nil {
			fmt.Fprintf(stderr, "build-brief: render raw output: %v\n", err)
			return 1
		}
		trackRun(tracking.Record{
			Timestamp:     timeNow(),
			ProjectPath:   command.ProjectDir,
			Command:       trackingCommand,
			Mode:          opts.Mode,
			Success:       runResult.ExitCode == 0,
			RawTokens:     rawTokens,
			EmittedTokens: rawTokens,
			RawLogPath:    runResult.RawLogPath,
		}, stderr)
	default:
		summary, err := reducer.Reduce(command, runResult)
		if err != nil {
			fmt.Fprintf(stderr, "build-brief: reduce log output: %v\n", err)
			return 1
		}
		summary.RawOutputTokens = rawTokens

		rendered, err := renderSummary(summary)
		if err != nil {
			fmt.Fprintf(stderr, "build-brief: render summary: %v\n", err)
			return 1
		}
		summary.EmittedTokens = tracking.EstimateTokens(rendered)
		summary.SavedTokens = tracking.SavedTokens(summary.RawOutputTokens, summary.EmittedTokens)
		summary.SavingsPct = tracking.SavingsPct(summary.RawOutputTokens, summary.EmittedTokens)
		if _, err := io.WriteString(stdout, rendered); err != nil {
			fmt.Fprintf(stderr, "build-brief: write summary: %v\n", err)
			return 1
		}
		trackRun(tracking.Record{
			Timestamp:     timeNow(),
			ProjectPath:   command.ProjectDir,
			Command:       trackingCommand,
			Mode:          opts.Mode,
			Success:       summary.Success,
			RawTokens:     summary.RawOutputTokens,
			EmittedTokens: summary.EmittedTokens,
			RawLogPath:    summary.RawLogPath,
			FailedTasks:   len(summary.FailedTasks),
			PassedTests:   summary.PassedTestCount,
			FailedTests:   summary.FailedTestCount,
		}, stderr)
	}

	return runResult.ExitCode
}

func parseArgs(args []string) (Options, []string, error) {
	opts := Options{
		Mode:           envOrDefault("BUILD_BRIEF_MODE", "human"),
		ProjectDir:     os.Getenv("BUILD_BRIEF_PROJECT_DIR"),
		GradlePath:     os.Getenv("BUILD_BRIEF_GRADLE_PATH"),
		LogDir:         os.Getenv("BUILD_BRIEF_LOG_DIR"),
		GradleUserHome: os.Getenv("BUILD_BRIEF_GRADLE_USER_HOME"),
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--":
			normalized, err := normalizeMode(opts)
			return normalized, args[i+1:], err
		case arg == "--help" || arg == "-h":
			opts.Help = true
		case arg == "--version":
			opts.Version = true
		case arg == "--install":
			opts.Install = true
		case arg == "--install-force":
			opts.InstallForce = true
		case arg == "--global":
			opts.Global = true
		case strings.HasPrefix(arg, "--mode="):
			opts.Mode = strings.TrimPrefix(arg, "--mode=")
		case arg == "--mode":
			value, next, err := nextArg(args, i, "--mode")
			if err != nil {
				return Options{}, nil, err
			}
			opts.Mode = value
			i = next
		case strings.HasPrefix(arg, "--project-dir="):
			opts.ProjectDir = strings.TrimPrefix(arg, "--project-dir=")
		case arg == "--project-dir":
			value, next, err := nextArg(args, i, "--project-dir")
			if err != nil {
				return Options{}, nil, err
			}
			opts.ProjectDir = value
			i = next
		case strings.HasPrefix(arg, "--gradle="):
			opts.GradlePath = strings.TrimPrefix(arg, "--gradle=")
		case arg == "--gradle":
			value, next, err := nextArg(args, i, "--gradle")
			if err != nil {
				return Options{}, nil, err
			}
			opts.GradlePath = value
			i = next
		case strings.HasPrefix(arg, "--log-dir="):
			opts.LogDir = strings.TrimPrefix(arg, "--log-dir=")
		case arg == "--log-dir":
			value, next, err := nextArg(args, i, "--log-dir")
			if err != nil {
				return Options{}, nil, err
			}
			opts.LogDir = value
			i = next
		case strings.HasPrefix(arg, "--gradle-user-home="):
			opts.GradleUserHome = strings.TrimPrefix(arg, "--gradle-user-home=")
		case arg == "--gradle-user-home":
			value, next, err := nextArg(args, i, "--gradle-user-home")
			if err != nil {
				return Options{}, nil, err
			}
			opts.GradleUserHome = value
			i = next
		default:
			if strings.HasPrefix(arg, "-") {
				return Options{}, nil, fmt.Errorf("unknown build-brief flag %q (use -- to pass Gradle flags through)", arg)
			}
			normalized, err := normalizeMode(opts)
			return normalized, args[i:], err
		}
	}

	normalized, err := normalizeMode(opts)
	return normalized, nil, err
}

func normalizeMode(opts Options) (Options, error) {
	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	if mode == "" {
		mode = "human"
	}

	switch mode {
	case "human", "raw":
		opts.Mode = mode
	case "json":
		opts.Mode = "human"
	default:
		return Options{}, fmt.Errorf("invalid mode %q (expected human or raw)", opts.Mode)
	}

	if opts.Global && opts.InstallForce {
		return Options{}, fmt.Errorf("--install-force cannot be combined with --global (build-brief only updates existing global instruction files)")
	}

	if opts.Global && opts.Install {
		return Options{}, fmt.Errorf("--install cannot be combined with --global (--install is local-only; use --global by itself for interactive global installs)")
	}

	return opts, nil
}

func nextArg(args []string, index int, flag string) (string, int, error) {
	next := index + 1
	if next >= len(args) {
		return "", index, fmt.Errorf("missing value for %s", flag)
	}

	return args[next], next, nil
}

func envOrDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func writeUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  build-brief [build-brief flags] [gradle|./gradlew|PATH-TO-GRADLE] [gradle task/args...]")
	fmt.Fprintln(w, "  build-brief [build-brief flags] [gradle task/args...]")
	fmt.Fprintln(w, "  build-brief [build-brief flags] -- [gradle flags/tasks...]")
	fmt.Fprintln(w, "  build-brief gains [--project] [--history] [--format text|json]")
	fmt.Fprintln(w, "  build-brief gains --reset")
	fmt.Fprintln(w, "  build-brief rewrite <shell command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Core flags:")
	fmt.Fprintln(w, "  --mode [human|raw]        Output mode (default: human)")
	fmt.Fprintln(w, "  --project-dir PATH        Project directory to run in")
	fmt.Fprintln(w, "  --gradle PATH             Explicit gradle/gradlew path")
	fmt.Fprintln(w, "  --gradle-user-home PATH   Shared Gradle user home for Gradle caches")
	fmt.Fprintln(w, "  --log-dir PATH            Directory for retained raw logs")
	fmt.Fprintln(w, "  --version                 Show build-brief version")
	fmt.Fprintln(w, "  --help, -h                Show this help")
	fmt.Fprintln(w, "  --install                 Local-only: append build-brief instructions to AGENTS.md in the current directory")
	fmt.Fprintln(w, "  --install-force           Create AGENTS.md if needed for local install, then install instructions")
	fmt.Fprintln(w, "  --global                  Global-only: detect supported AI tools and update existing global instruction files interactively")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common commands:")
	fmt.Fprintln(w, "  build-brief test")
	fmt.Fprintln(w, "  build-brief build")
	fmt.Fprintln(w, "  build-brief gradle test")
	fmt.Fprintln(w, "  build-brief ./gradlew test")
	fmt.Fprintln(w, "  build-brief --gradle-user-home /tmp/build-brief-gradle-home ./gradlew test")
	fmt.Fprintln(w, "  build-brief -- --stacktrace test")
	fmt.Fprintln(w, "  build-brief --install")
	fmt.Fprintln(w, "  build-brief --install-force")
	fmt.Fprintln(w, "  build-brief --global")
	fmt.Fprintln(w, "  build-brief gains --history")
	fmt.Fprintln(w, "  build-brief rewrite 'gradle test'")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Install behavior:")
	fmt.Fprintln(w, "  --install")
	fmt.Fprintln(w, "      Updates AGENTS.md in the current directory.")
	fmt.Fprintln(w, "      Fails if AGENTS.md does not exist.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  --install-force")
	fmt.Fprintln(w, "      Local-only force mode.")
	fmt.Fprintln(w, "      Creates AGENTS.md if needed, then installs the managed build-brief block.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  --global")
	fmt.Fprintln(w, "      Detects supported AI tools by binary and known global instruction-file paths.")
	fmt.Fprintln(w, "      Shows a numbered list and asks which tools to update.")
	fmt.Fprintln(w, "      Selection accepts comma-separated numbers, '*' or 'all', or blank to cancel.")
	fmt.Fprintln(w, "      Only existing global instruction files are updated; OpenCode also installs a managed plugin file.")
	fmt.Fprintln(w, "      Must be used by itself; do not combine it with --install or --install-force.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Supported global tool registry:")
	fmt.Fprintln(w, "  GitHub Copilot CLI, Claude Code, Codex CLI, OpenCode, Gemini CLI")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Rewrite command:")
	fmt.Fprintln(w, "  build-brief rewrite")
	fmt.Fprintln(w, "      Rewrites routine Gradle shell commands to build-brief-compatible commands.")
	fmt.Fprintln(w, "      Intended for hooks/plugins such as the OpenCode tool.execute.before hook.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Gains command:")
	fmt.Fprintln(w, "  build-brief gains")
	fmt.Fprintln(w, "      Shows cumulative token savings based on raw Gradle output vs emitted build-brief output.")
	fmt.Fprintln(w, "  build-brief gains --project")
	fmt.Fprintln(w, "      Filters savings to the current project directory.")
	fmt.Fprintln(w, "  build-brief gains --history")
	fmt.Fprintln(w, "      Includes recent recorded runs.")
	fmt.Fprintln(w, "  build-brief gains --format json")
	fmt.Fprintln(w, "      Emits structured savings data for tooling.")
	fmt.Fprintln(w, "  build-brief gains --reset")
	fmt.Fprintln(w, "      Clears recorded gains history.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Environment overrides:")
	fmt.Fprintln(w, "  BUILD_BRIEF_MODE")
	fmt.Fprintln(w, "  BUILD_BRIEF_PROJECT_DIR")
	fmt.Fprintln(w, "  BUILD_BRIEF_GRADLE_PATH")
	fmt.Fprintln(w, "  BUILD_BRIEF_GRADLE_USER_HOME")
	fmt.Fprintln(w, "  BUILD_BRIEF_LOG_DIR")
}

type gainsOptions struct {
	Project bool
	History bool
	Format  string
	Reset   bool
}

func runGains(args []string, stdout, stderr io.Writer) int {
	opts, err := parseGainsArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: %v\n", err)
		return 2
	}

	projectDir := ""
	if opts.Project {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "build-brief: resolve current directory: %v\n", err)
			return 1
		}
		projectDir = wd
	}

	if opts.Reset {
		if opts.Project || opts.History || opts.Format != "text" {
			fmt.Fprintln(stderr, "build-brief: --reset cannot be combined with --project, --history, or --format")
			return 2
		}
		if err := tracking.Reset(); err != nil {
			fmt.Fprintf(stderr, "build-brief: reset gains: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "build-brief gains reset.")
		return 0
	}

	report, err := tracking.LoadReport(projectDir, opts.History)
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: load gains: %v\n", err)
		return 1
	}

	if opts.Format == "json" {
		if err := tracking.RenderJSON(stdout, report); err != nil {
			fmt.Fprintf(stderr, "build-brief: render gains json: %v\n", err)
			return 1
		}
		return 0
	}

	if err := tracking.RenderText(stdout, report, opts.History); err != nil {
		fmt.Fprintf(stderr, "build-brief: render gains text: %v\n", err)
		return 1
	}

	return 0
}

func parseGainsArgs(args []string) (gainsOptions, error) {
	opts := gainsOptions{Format: "text"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--project":
			opts.Project = true
		case arg == "--history":
			opts.History = true
		case arg == "--reset":
			opts.Reset = true
		case strings.HasPrefix(arg, "--format="):
			opts.Format = strings.TrimPrefix(arg, "--format=")
		case arg == "--format":
			value, next, err := nextArg(args, i, "--format")
			if err != nil {
				return gainsOptions{}, err
			}
			opts.Format = value
			i = next
		default:
			return gainsOptions{}, fmt.Errorf("unknown gains flag %q", arg)
		}
	}

	switch strings.ToLower(strings.TrimSpace(opts.Format)) {
	case "", "text":
		opts.Format = "text"
	case "json":
		opts.Format = "json"
	default:
		return gainsOptions{}, fmt.Errorf("invalid gains format %q (expected text or json)", opts.Format)
	}

	return opts, nil
}

func renderSummary(summary reducer.Summary) (string, error) {
	var buffer bytes.Buffer
	if err := output.RenderHuman(&buffer, summary); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func formatElapsed(duration time.Duration) string {
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Round(time.Second).Seconds()))
	}
	if duration < time.Hour {
		minutes := duration / time.Minute
		seconds := (duration % time.Minute) / time.Second
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	hours := duration / time.Hour
	minutes := (duration % time.Hour) / time.Minute
	return fmt.Sprintf("%dh%02dm", hours, minutes)
}

func trackRun(record tracking.Record, stderr io.Writer) {
	record.SavedTokens = tracking.SavedTokens(record.RawTokens, record.EmittedTokens)
	record.SavingsPct = tracking.SavingsPct(record.RawTokens, record.EmittedTokens)
	if err := tracking.RecordRun(record); err != nil {
		fmt.Fprintf(stderr, "build-brief: track gains: %v\n", err)
	}
}

var timeNow = time.Now

func runLocalInstall(stdout, stderr io.Writer, force bool) int {
	dir, err := currentDir()
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: resolve current directory: %v\n", err)
		return 1
	}

	target, err := install.InstallLocal(dir, force)
	if err != nil {
		if install.MissingAgentsError(err) {
			fmt.Fprintf(stderr, "build-brief: AGENTS.md not found in %s\n", dir)
			fmt.Fprintln(stderr, "Run this command from a project directory that already has AGENTS.md, or use --install-force to create one.")
			return 2
		}
		fmt.Fprintf(stderr, "build-brief: install instructions: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Installed build-brief instructions into %s\n", target)
	return 0
}

func runGlobalInstall(stdin io.Reader, stdout, stderr io.Writer) int {
	detected, err := install.DetectGlobalTools()
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: detect AI tools: %v\n", err)
		return 1
	}
	if len(detected) == 0 {
		fmt.Fprintln(stderr, "build-brief: no supported AI tools detected")
		return 1
	}

	fmt.Fprintln(stdout, "Detected AI tools and global instruction targets:")
	selected, err := install.PromptForSelection(stdin, stdout, detected)
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: read selection: %v\n", err)
		return 2
	}
	if len(selected) == 0 {
		fmt.Fprintln(stdout, "No tools selected. Nothing changed.")
		return 0
	}

	installed, failures := install.InstallGlobal(selected)
	for _, item := range installed {
		fmt.Fprintf(stdout, "Installed build-brief instructions into %s\n", item)
	}
	for _, failure := range failures {
		fmt.Fprintf(stderr, "build-brief: %v\n", failure)
	}

	if len(installed) == 0 && len(failures) > 0 {
		return 1
	}
	return 0
}

func runRewrite(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "build-brief: rewrite requires a shell command")
		return 2
	}

	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "build-brief: rewrite requires a shell command")
		return 2
	}

	command := strings.Join(args, " ")
	rewritten, _ := rewrite.ShellCommand(command)
	fmt.Fprintln(stdout, rewritten)
	return 0
}
