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

func ApplyStableArgs(args []string) []string {
	if hasConsoleFlag(args) {
		return append([]string{}, args...)
	}

	withConsole := []string{"--console=plain"}
	return append(withConsole, args...)
}

func (c Command) DisplayLine() string {
	return strings.Join(append([]string{filepath.Base(c.Executable)}, c.Args...), " ")
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
