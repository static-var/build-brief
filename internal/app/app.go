package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"build-brief/internal/gradle"
	"build-brief/internal/output"
	"build-brief/internal/reducer"
	"build-brief/internal/runner"
)

type Options struct {
	Mode       string
	ProjectDir string
	GradlePath string
	LogDir     string
	Help       bool
	Version    bool
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
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

	command, err := gradle.Resolve(opts.ProjectDir, opts.GradlePath)
	if err != nil {
		fmt.Fprintf(stderr, "build-brief: %v\n", err)
		return 1
	}
	command.Args = gradle.ApplyStableArgs(gradleArgs)

	runResult, err := runner.Run(ctx, command, opts.LogDir)
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

	switch opts.Mode {
	case "raw":
		if err := output.RenderRaw(stdout, runResult.RawLogPath); err != nil {
			fmt.Fprintf(stderr, "build-brief: render raw output: %v\n", err)
			return 1
		}
	default:
		summary, err := reducer.Reduce(command, runResult)
		if err != nil {
			fmt.Fprintf(stderr, "build-brief: reduce log output: %v\n", err)
			return 1
		}
		var renderErr error
		if opts.Mode == "json" {
			renderErr = output.RenderJSON(stdout, summary)
		} else {
			renderErr = output.RenderHuman(stdout, summary)
		}
		if renderErr != nil {
			fmt.Fprintf(stderr, "build-brief: render summary: %v\n", renderErr)
			return 1
		}
	}

	return runResult.ExitCode
}

func parseArgs(args []string) (Options, []string, error) {
	opts := Options{
		Mode:       envOrDefault("BUILD_BRIEF_MODE", "human"),
		ProjectDir: os.Getenv("BUILD_BRIEF_PROJECT_DIR"),
		GradlePath: os.Getenv("BUILD_BRIEF_GRADLE_PATH"),
		LogDir:     os.Getenv("BUILD_BRIEF_LOG_DIR"),
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
	case "human", "json", "raw":
		opts.Mode = mode
		return opts, nil
	default:
		return Options{}, fmt.Errorf("invalid mode %q (expected human, json, or raw)", opts.Mode)
	}
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
	fmt.Fprintln(w, "  build-brief [build-brief flags] [gradle task/args...]")
	fmt.Fprintln(w, "  build-brief [build-brief flags] -- [gradle flags/tasks...]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "build-brief flags:")
	fmt.Fprintln(w, "  --mode [human|json|raw]   Output mode (default: human)")
	fmt.Fprintln(w, "  --project-dir PATH        Project directory to run in")
	fmt.Fprintln(w, "  --gradle PATH             Explicit gradle/gradlew path")
	fmt.Fprintln(w, "  --log-dir PATH            Directory for retained raw logs")
	fmt.Fprintln(w, "  --version                 Show build-brief version")
	fmt.Fprintln(w, "  --help, -h                Show this help")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Environment overrides:")
	fmt.Fprintln(w, "  BUILD_BRIEF_MODE")
	fmt.Fprintln(w, "  BUILD_BRIEF_PROJECT_DIR")
	fmt.Fprintln(w, "  BUILD_BRIEF_GRADLE_PATH")
	fmt.Fprintln(w, "  BUILD_BRIEF_LOG_DIR")
}
