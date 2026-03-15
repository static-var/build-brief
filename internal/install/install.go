package install

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	blockStart = "<!-- build-brief:instructions:start -->"
	blockEnd   = "<!-- build-brief:instructions:end -->"
)

var errMissingAgents = errors.New("AGENTS.md not found")

var runRTKHelp = func() error {
	cmd := exec.Command("rtk", "--help")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

type Tool struct {
	ID                 string
	Name               string
	Binaries           []string
	GlobalTargets      []string
	LocalInstruction   []string
	SupportsHookGuides bool
	SupportsPlugin     bool
}

type DetectedTool struct {
	Tool             Tool
	DetectedBinary   string
	ExistingTargets  []string
	PreferredTarget  string
	DetectionReasons []string
}

func DetectGlobalTools() ([]DetectedTool, error) {
	tools, err := knownTools()
	if err != nil {
		return nil, err
	}

	detected := make([]DetectedTool, 0, len(tools))
	for _, tool := range tools {
		entry := DetectedTool{Tool: tool}

		for _, binary := range tool.Binaries {
			path, err := exec.LookPath(binary)
			if err == nil {
				entry.DetectedBinary = path
				entry.DetectionReasons = append(entry.DetectionReasons, "binary:"+binary)
				break
			}
		}

		for _, target := range tool.GlobalTargets {
			if fileExists(target) {
				entry.ExistingTargets = append(entry.ExistingTargets, target)
				entry.DetectionReasons = append(entry.DetectionReasons, "file:"+target)
			}
		}

		if len(entry.ExistingTargets) > 0 {
			entry.PreferredTarget = entry.ExistingTargets[0]
		} else if len(tool.GlobalTargets) > 0 {
			entry.PreferredTarget = tool.GlobalTargets[0]
		}

		if entry.DetectedBinary != "" || len(entry.ExistingTargets) > 0 {
			detected = append(detected, entry)
		}
	}

	return detected, nil
}

func InstallLocal(currentDir string, force bool) (string, error) {
	target := filepath.Join(currentDir, "AGENTS.md")
	if !fileExists(target) && !force {
		return "", fmt.Errorf("%w in %s", errMissingAgents, currentDir)
	}

	return target, upsertInstructionBlock(target, localInstructions(), force)
}

func InstallGlobal(selected []DetectedTool) ([]string, []error) {
	installed := make([]string, 0, len(selected))
	failures := make([]error, 0)

	for _, tool := range selected {
		pluginInstalled := false
		if tool.Tool.SupportsPlugin {
			switch tool.Tool.ID {
			case "opencode":
				target, err := installOpenCodePlugin(tool)
				if err != nil {
					failures = append(failures, fmt.Errorf("%s plugin: %w", tool.Tool.Name, err))
				} else {
					installed = append(installed, fmt.Sprintf("%s plugin -> %s", tool.Tool.Name, target))
					pluginInstalled = true
				}
			}
		}

		target := tool.PreferredTarget
		if target == "" {
			if !pluginInstalled {
				failures = append(failures, fmt.Errorf("%s: no known global instruction path", tool.Tool.Name))
			}
			continue
		}

		if !fileExists(target) {
			if !pluginInstalled {
				failures = append(failures, fmt.Errorf("%s: global instruction file not found at %s (create it manually, then rerun --global)", tool.Tool.Name, target))
			}
			continue
		}

		if err := upsertInstructionBlock(target, globalInstructions(tool.Tool), false); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", tool.Tool.Name, err))
			continue
		}

		installed = append(installed, fmt.Sprintf("%s -> %s", tool.Tool.Name, target))
	}

	return installed, failures
}

func PromptForSelection(in io.Reader, out io.Writer, tools []DetectedTool) ([]DetectedTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	for i, tool := range tools {
		hookLabel := toolModeLabel(tool.Tool)

		status := "global file missing (build-brief will not create it)"
		if fileExists(tool.PreferredTarget) {
			status = "existing global file"
		} else if tool.Tool.SupportsPlugin {
			status = "plugin will be created; instruction file stays optional"
		}

		fmt.Fprintf(out, "[%d] %s\n", i+1, tool.Tool.Name)
		fmt.Fprintf(out, "    target: %s\n", tool.PreferredTarget)
		fmt.Fprintf(out, "    mode: %s, %s\n", hookLabel, status)
	}
	fmt.Fprint(out, "Select tools (comma-separated numbers, '*' for all, blank to cancel): ")

	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	if line == "*" || strings.EqualFold(line, "all") {
		return tools, nil
	}

	parts := strings.Split(line, ",")
	selected := make([]DetectedTool, 0, len(parts))
	seen := make(map[int]struct{})
	for _, part := range parts {
		indexText := strings.TrimSpace(part)
		index, err := strconv.Atoi(indexText)
		if err != nil || index < 1 || index > len(tools) {
			return nil, fmt.Errorf("invalid selection %q", indexText)
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		selected = append(selected, tools[index-1])
	}

	return selected, nil
}

func MissingAgentsError(err error) bool {
	return errors.Is(err, errMissingAgents)
}

func RTKInstalled() bool {
	return runRTKHelp() == nil
}

func RTKInstallNotice() string {
	return strings.TrimSpace(`
RTK detected on this machine.

RTK can intercept Gradle commands before build-brief gets a chance to rewrite or wrap them.

If you want build-brief to keep priority for Gradle work, consider adding guidance like:
- Prefer build-brief over RTK, raw gradle, and ./gradlew for Gradle commands.
- Let raw gradle/./gradlew commands be rewritten by build-brief hooks or plugins instead of wrapping Gradle in RTK first.
`)
}

func knownTools() ([]Tool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(home, ".config")
	}
	legacyConfigDir := filepath.Join(home, ".config")

	return []Tool{
		{
			ID:               "copilot-cli",
			Name:             "GitHub Copilot CLI",
			Binaries:         []string{"copilot"},
			GlobalTargets:    []string{filepath.Join(home, ".copilot", "copilot-instructions.md")},
			LocalInstruction: []string{"AGENTS.md", ".github/copilot-instructions.md", ".github/instructions/*.instructions.md", "CLAUDE.md", "GEMINI.md"},
		},
		{
			ID:                 "claude-code",
			Name:               "Claude Code",
			Binaries:           []string{"claude"},
			GlobalTargets:      []string{filepath.Join(home, ".claude", "CLAUDE.md")},
			LocalInstruction:   []string{"CLAUDE.md"},
			SupportsHookGuides: true,
		},
		{
			ID:       "codex-cli",
			Name:     "Codex CLI",
			Binaries: []string{"codex"},
			GlobalTargets: appendConfigTargets(
				[]string{filepath.Join(home, ".codex", "AGENTS.md")},
				configDir,
				legacyConfigDir,
				"codex",
				"AGENTS.md",
			),
			LocalInstruction: []string{"AGENTS.md", "CODEX.md"},
		},
		{
			ID:                 "opencode",
			Name:               "OpenCode",
			Binaries:           []string{"opencode"},
			GlobalTargets:      []string{filepath.Join(home, ".config", "opencode", "AGENTS.md"), filepath.Join(home, ".opencode", "AGENTS.md")},
			LocalInstruction:   []string{"AGENTS.md"},
			SupportsHookGuides: true,
			SupportsPlugin:     true,
		},
		{
			ID:               "gemini-cli",
			Name:             "Gemini CLI",
			Binaries:         []string{"gemini"},
			GlobalTargets:    []string{filepath.Join(home, ".gemini", "GEMINI.md")},
			LocalInstruction: []string{"GEMINI.md", "AGENTS.md"},
		},
	}, nil
}

func appendConfigTargets(targets []string, configDir, legacyConfigDir, appName, fileName string) []string {
	primary := filepath.Join(configDir, appName, fileName)
	targets = append(targets, primary)

	legacy := filepath.Join(legacyConfigDir, appName, fileName)
	if legacy != primary {
		targets = append(targets, legacy)
	}

	return targets
}

func toolModeLabel(tool Tool) string {
	parts := []string{"rules"}
	if tool.SupportsHookGuides {
		parts = append(parts, "hook guidance")
	}
	if tool.SupportsPlugin {
		parts = append(parts, "plugin install")
	}
	return strings.Join(parts, " + ")
}

func localInstructions() string {
	return strings.TrimSpace(fmt.Sprintf(`
%s
## build-brief

- Prefer `+"`build-brief gradle ...`"+` for PATH Gradle and `+"`build-brief ./gradlew ...`"+` for the project wrapper.
- Use default `+"`build-brief`"+` output for routine Gradle work; it stays intentionally short on clean success cases.
- Use `+"`build-brief gradle --stacktrace ...`"+` or `+"`build-brief ./gradlew --stacktrace ...`"+` when you need Gradle stack traces.
- Do not add Gradle log-silencing flags like `+"`--quiet`"+`, `+"`--warn`"+`, or `+"`--warning-mode none`"+`; `+"`build-brief`"+` already reduces output and needs the normal signal.
- `+"`build-brief`"+` always runs Gradle with `+"`--no-daemon`"+`; do not add `+"`--daemon`"+`.
- Preserve the raw log path from `+"`build-brief`"+` output when handing build failures to another tool or agent.
%s
`, blockStart, blockEnd))
}

func globalInstructions(tool Tool) string {
	lines := []string{
		blockStart,
		"## build-brief",
		"",
		"- Prefer `build-brief gradle ...` for PATH Gradle and `build-brief ./gradlew ...` for the project wrapper.",
		"- Use the default `build-brief` output for routine Gradle work; clean success cases stay intentionally short.",
		"- Use `build-brief gradle --stacktrace ...` or `build-brief ./gradlew --stacktrace ...` when you need Gradle stack traces.",
		"- Do not add Gradle log-silencing flags like `--quiet`, `--warn`, or `--warning-mode none`; `build-brief` already reduces output and needs the normal signal.",
		"- `build-brief` always runs Gradle with `--no-daemon`; do not add `--daemon`.",
		"- Keep the raw log path in your response when build diagnostics may need deeper inspection.",
	}

	if tool.SupportsHookGuides {
		lines = append(lines,
			"",
			"### Hook guidance",
			"",
			"- If this tool supports command hooks, add a guardrail that reminds or rewrites routine `gradle`/`./gradlew` usage to `build-brief gradle ...` or `build-brief ./gradlew ...` where safe.",
			"- Keep raw Gradle available as a fallback for unusual cases or when full unfiltered output is explicitly needed.",
		)
	}

	if tool.SupportsPlugin {
		lines = append(lines,
			"",
			"### Plugin guidance",
			"",
			"- The managed OpenCode plugin rewrites routine `gradle` and `./gradlew` shell commands to explicit `build-brief gradle ...` or `build-brief ./gradlew ...` commands before execution.",
			"- Keep using raw Gradle intentionally only when you want to bypass that reduction layer.",
		)
	}

	lines = append(lines, blockEnd)
	return strings.Join(lines, "\n")
}

func installOpenCodePlugin(tool DetectedTool) (string, error) {
	baseDir := filepath.Dir(tool.PreferredTarget)
	if baseDir == "." || baseDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		baseDir = filepath.Join(home, ".config", "opencode")
	}

	target := filepath.Join(baseDir, "plugins", "build-brief.ts")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, []byte(openCodePluginSource()), 0o644); err != nil {
		return "", err
	}
	return target, nil
}

func openCodePluginSource() string {
	lines := []string{
		`import type { Plugin } from "@opencode-ai/plugin"`,
		``,
		`// build-brief OpenCode plugin — rewrites routine Gradle shell commands`,
		`// to build-brief before execution.`,
		`//`,
		`// All rewrite logic lives in "build-brief rewrite" so other CLIs can`,
		`// reuse the same behavior without duplicating parsing rules here.`,
		``,
		`export const BuildBriefOpenCodePlugin: Plugin = async ({ $ }) => {`,
		`  try {`,
		`    await $` + "`command -v build-brief`" + `.quiet()`,
		`  } catch {`,
		`    console.warn("[build-brief] build-brief binary not found in PATH — plugin disabled")`,
		`    return {}`,
		`  }`,
		``,
		`  return {`,
		`    "tool.execute.before": async (input, output) => {`,
		`      const tool = String(input?.tool ?? "").toLowerCase()`,
		`      if (tool !== "bash" && tool !== "shell") return`,
		``,
		`      const args = output?.args`,
		`      if (!args || typeof args !== "object") return`,
		``,
		`      const command = (args as Record<string, unknown>).command`,
		`      if (typeof command !== "string" || !command.trim()) return`,
		`      if (command.includes("build-brief")) return`,
		``,
		`      try {`,
		`        const result = await $` + "`build-brief rewrite ${command}`" + `.quiet().nothrow()`,
		`        if (result.exitCode !== 0) return`,
		``,
		`        const rewritten = String(result.stdout).trim()`,
		`        if (rewritten && rewritten !== command.trim()) {`,
		`          ;(args as Record<string, unknown>).command = rewritten`,
		`        }`,
		`      } catch {`,
		`        // Rewrite failed — pass through unchanged.`,
		`      }`,
		`    },`,
		`  }`,
		`}`,
	}

	return strings.Join(lines, "\n") + "\n"
}

func upsertInstructionBlock(path, block string, force bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	content := ""
	if fileExists(path) {
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content = string(existing)
	} else if !force {
		return os.ErrNotExist
	}

	if strings.Contains(content, blockStart) && strings.Contains(content, blockEnd) {
		start := strings.Index(content, blockStart)
		end := strings.Index(content, blockEnd)
		if end > start {
			end += len(blockEnd)
			content = content[:start] + block + content[end:]
		}
	} else if strings.TrimSpace(content) == "" {
		content = block + "\n"
	} else {
		content = strings.TrimRight(content, "\n") + "\n\n" + block + "\n"
	}

	return os.WriteFile(path, []byte(content), 0o644)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
