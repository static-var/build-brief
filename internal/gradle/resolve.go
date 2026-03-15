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

type DaemonMode string

const (
	DaemonModeAuto DaemonMode = "auto"
	DaemonModeOn   DaemonMode = "on"
	DaemonModeOff  DaemonMode = "off"
)

type StableArgsOptions struct {
	DaemonMode     DaemonMode
	GradleUserHome string
}

func Resolve(projectDir, explicitPath string) (Command, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return Command{}, fmt.Errorf("resolve project directory: %w", err)
	}

	if explicitPath != "" {
		absGradle, err := filepath.Abs(explicitPath)
		if err != nil {
			return Command{}, fmt.Errorf("resolve explicit Gradle path: %w", err)
		}

		if _, err := os.Stat(absGradle); err != nil {
			return Command{}, fmt.Errorf("explicit Gradle path %q is not available: %w", absGradle, err)
		}

		return Command{
			Executable: absGradle,
			ProjectDir: absProjectDir,
			Source:     SourceExplicit,
		}, nil
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
		switch normalizeDaemonMode(opts.DaemonMode) {
		case DaemonModeOn:
			stable = append(stable, "--daemon")
		case DaemonModeOff:
			stable = append(stable, "--no-daemon")
		}
	}

	return append(stable, args...)
}

func (c Command) DisplayLine() string {
	return strings.Join(append([]string{filepath.Base(c.Executable)}, c.Args...), " ")
}

func (c Command) TrackingLine() string {
	filtered := make([]string, 0, len(c.Args))
	skipNext := false
	for _, arg := range c.Args {
		if skipNext {
			skipNext = false
			continue
		}

		switch {
		case arg == "--console" || strings.HasPrefix(arg, "--console="):
			continue
		case arg == "--daemon" || arg == "--no-daemon":
			continue
		case arg == "--gradle-user-home":
			skipNext = true
			continue
		case strings.HasPrefix(arg, "--gradle-user-home="):
			continue
		default:
			filtered = append(filtered, arg)
		}
	}

	return strings.Join(append([]string{filepath.Base(c.Executable)}, filtered...), " ")
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

func normalizeDaemonMode(mode DaemonMode) DaemonMode {
	switch strings.ToLower(strings.TrimSpace(string(mode))) {
	case "", string(DaemonModeAuto):
		return DaemonModeAuto
	case string(DaemonModeOn):
		return DaemonModeOn
	case string(DaemonModeOff):
		return DaemonModeOff
	default:
		return DaemonModeAuto
	}
}
