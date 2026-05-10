package install

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/term"
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

var userHomeDir = os.UserHomeDir
var userConfigDir = os.UserConfigDir
var termIsTerminal = func(fd int) bool {
	return term.IsTerminal(fd)
}
var termMakeRaw = func(fd int) (*term.State, error) {
	return term.MakeRaw(fd)
}
var termRestore = func(fd int, state *term.State) error {
	return term.Restore(fd, state)
}
var runClaudePluginInstall = func(binary, marketplaceDir, pluginRef string) error {
	addOutput, addErr := exec.Command(binary, "plugin", "marketplace", "add", marketplaceDir).CombinedOutput()
	if addErr != nil {
		message := strings.TrimSpace(string(addOutput))
		if message != "" && !strings.Contains(strings.ToLower(message), "already") {
			return fmt.Errorf("%w: %s", addErr, message)
		}
		if message == "" && addErr != nil {
			return addErr
		}
	}

	installOutput, installErr := exec.Command(binary, "plugin", "install", pluginRef).CombinedOutput()
	if installErr == nil {
		return nil
	}

	message := strings.TrimSpace(string(installOutput))
	if message == "" {
		return installErr
	}

	return fmt.Errorf("%w: %s", installErr, message)
}
var runCopilotPluginInstall = func(binary, pluginDir string) error {
	output, err := exec.Command(binary, "plugin", "install", pluginDir).CombinedOutput()
	if err == nil {
		return nil
	}

	message := strings.TrimSpace(string(output))
	if message == "" {
		return err
	}

	return fmt.Errorf("%w: %s", err, message)
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

type selectionKey int

const (
	selectionKeyUnknown selectionKey = iota
	selectionKeyUp
	selectionKeyDown
	selectionKeyToggle
	selectionKeySubmit
	selectionKeyCancel
)

type selectionMenu struct {
	tools    []DetectedTool
	cursor   int
	selected []bool
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

	return target, upsertInstructionBlock(target, localInstructions(RTKInstalled()), force)
}

func InstallGlobal(selected []DetectedTool) ([]string, []error) {
	installed := make([]string, 0, len(selected))
	failures := make([]error, 0)
	rtkInstalled := RTKInstalled()

	for _, tool := range selected {
		pluginInstalled := false
		if tool.Tool.SupportsPlugin {
			switch tool.Tool.ID {
			case "copilot-cli":
				target, err := installCopilotPlugin(tool)
				if err != nil {
					failures = append(failures, fmt.Errorf("%s plugin: %w", tool.Tool.Name, err))
				} else {
					installed = append(installed, fmt.Sprintf("%s plugin -> %s", tool.Tool.Name, target))
					pluginInstalled = true
				}
			case "claude-code":
				target, err := installClaudePlugin(tool)
				if err != nil {
					failures = append(failures, fmt.Errorf("%s plugin: %w", tool.Tool.Name, err))
				} else {
					installed = append(installed, fmt.Sprintf("%s plugin -> %s", tool.Tool.Name, target))
					pluginInstalled = true
				}
			case "opencode":
				target, err := installOpenCodePlugin(tool)
				if err != nil {
					failures = append(failures, fmt.Errorf("%s plugin: %w", tool.Tool.Name, err))
				} else {
					installed = append(installed, fmt.Sprintf("%s plugin -> %s", tool.Tool.Name, target))
					pluginInstalled = true
				}
			case "codex-cli":
				target, err := installCodexPlugin(tool)
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

		if err := upsertInstructionBlock(target, globalInstructions(tool.Tool, rtkInstalled), false); err != nil {
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

	if inputFile, outputFile, ok := interactiveTerminalFiles(in, out); ok {
		return promptForSelectionInteractive(inputFile, outputFile, tools)
	}

	return promptForSelectionLineInput(in, out, tools)
}

func promptForSelectionLineInput(in io.Reader, out io.Writer, tools []DetectedTool) ([]DetectedTool, error) {
	for i, tool := range tools {
		hookLabel, status := selectionDisplay(tool)

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

func promptForSelectionInteractive(in *os.File, out *os.File, tools []DetectedTool) ([]DetectedTool, error) {
	fd := int(in.Fd())
	state, err := termMakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = termRestore(fd, state)
	}()

	menu := newSelectionMenu(tools)
	reader := bufio.NewReader(in)

	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(out, "\x1b[?25h\x1b[?1049l")

	for {
		if err := menu.render(out); err != nil {
			return nil, err
		}

		key, err := readSelectionKey(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return menu.selectedTools(), nil
			}
			return nil, err
		}

		done, cancel := menu.handleKey(key)
		if cancel {
			return nil, nil
		}
		if done {
			return menu.selectedTools(), nil
		}
	}
}

func interactiveTerminalFiles(in io.Reader, out io.Writer) (*os.File, *os.File, bool) {
	inputFile, inputOK := in.(*os.File)
	outputFile, outputOK := out.(*os.File)
	if !inputOK || !outputOK {
		return nil, nil, false
	}
	if !termIsTerminal(int(inputFile.Fd())) || !termIsTerminal(int(outputFile.Fd())) {
		return nil, nil, false
	}
	return inputFile, outputFile, true
}

func selectionDisplay(tool DetectedTool) (string, string) {
	hookLabel := toolModeLabel(tool.Tool)
	status := "global file missing (build-brief will not create it)"
	if fileExists(tool.PreferredTarget) {
		status = "existing global file"
	} else if tool.Tool.SupportsPlugin {
		status = "plugin will be created; instruction file stays optional"
	}
	return hookLabel, status
}

func newSelectionMenu(tools []DetectedTool) selectionMenu {
	return selectionMenu{
		tools:    tools,
		selected: make([]bool, len(tools)),
	}
}

func (m *selectionMenu) handleKey(key selectionKey) (bool, bool) {
	switch key {
	case selectionKeyUp:
		if len(m.tools) == 0 {
			return false, false
		}
		m.cursor--
		if m.cursor < 0 {
			m.cursor = len(m.tools) - 1
		}
	case selectionKeyDown:
		if len(m.tools) == 0 {
			return false, false
		}
		m.cursor++
		if m.cursor >= len(m.tools) {
			m.cursor = 0
		}
	case selectionKeyToggle:
		if len(m.selected) == 0 {
			return false, false
		}
		m.selected[m.cursor] = !m.selected[m.cursor]
	case selectionKeySubmit:
		return true, false
	case selectionKeyCancel:
		return false, true
	}
	return false, false
}

func (m selectionMenu) selectedTools() []DetectedTool {
	selected := make([]DetectedTool, 0, len(m.tools))
	for i, tool := range m.tools {
		if !m.selected[i] {
			continue
		}
		selected = append(selected, tool)
	}
	return selected
}

func (m selectionMenu) selectedCount() int {
	count := 0
	for _, selected := range m.selected {
		if selected {
			count++
		}
	}
	return count
}

func (m selectionMenu) render(out io.Writer) error {
	var buffer strings.Builder
	buffer.WriteString("\x1b[H\x1b[2J")
	buffer.WriteString("Detected AI tools and global instruction targets:\r\n")
	buffer.WriteString("Use Up/Down to move, Space to toggle, Enter to install, q to cancel.\r\n")
	buffer.WriteString("\r\n")

	for i, tool := range m.tools {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}

		mark := " "
		if m.selected[i] {
			mark = "x"
		}

		hookLabel, status := selectionDisplay(tool)
		target := tool.PreferredTarget
		if strings.TrimSpace(target) == "" {
			target = "(none)"
		}

		fmt.Fprintf(&buffer, "%s [%s] %s\r\n", cursor, mark, tool.Tool.Name)
		fmt.Fprintf(&buffer, "    target: %s\r\n", target)
		fmt.Fprintf(&buffer, "    mode: %s, %s\r\n\r\n", hookLabel, status)
	}

	fmt.Fprintf(&buffer, "Selected: %d\r\n", m.selectedCount())
	_, err := io.WriteString(out, buffer.String())
	return err
}

func readSelectionKey(reader *bufio.Reader) (selectionKey, error) {
	key, err := reader.ReadByte()
	if err != nil {
		return selectionKeyUnknown, err
	}

	switch key {
	case 'k':
		return selectionKeyUp, nil
	case 'j':
		return selectionKeyDown, nil
	case ' ':
		return selectionKeyToggle, nil
	case '\r', '\n':
		return selectionKeySubmit, nil
	case 'q', 3:
		return selectionKeyCancel, nil
	case 27:
		next, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return selectionKeyUnknown, nil
			}
			return selectionKeyUnknown, err
		}
		if next != '[' {
			return selectionKeyUnknown, nil
		}

		direction, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return selectionKeyUnknown, nil
			}
			return selectionKeyUnknown, err
		}

		switch direction {
		case 'A':
			return selectionKeyUp, nil
		case 'B':
			return selectionKeyDown, nil
		}
	}

	return selectionKeyUnknown, nil
}

func MissingAgentsError(err error) bool {
	return errors.Is(err, errMissingAgents)
}

func RTKInstalled() bool {
	return runRTKHelp() == nil
}

func knownTools() ([]Tool, error) {
	home, err := userHomeDir()
	if err != nil {
		return nil, err
	}

	configDir, err := userConfigDir()
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
			SupportsPlugin:   true,
		},
		{
			ID:                 "claude-code",
			Name:               "Claude Code",
			Binaries:           []string{"claude"},
			GlobalTargets:      []string{filepath.Join(home, ".claude", "CLAUDE.md")},
			LocalInstruction:   []string{"CLAUDE.md"},
			SupportsHookGuides: true,
			SupportsPlugin:     true,
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
			SupportsPlugin:   true,
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

func localInstructions(rtkInstalled bool) string {
	lines := []string{
		blockStart,
		"## build-brief",
		"",
		"- Prefer `build-brief gradle ...` for PATH Gradle and `build-brief ./gradlew ...` for the project wrapper.",
		"- For chained shell commands, rewrite each Gradle segment individually, for example `build-brief gradle test && build-brief gradle check`.",
		"- Use default `build-brief` output for routine Gradle work; it stays intentionally short on clean success cases.",
		"- Use `build-brief gradle --stacktrace ...` or `build-brief ./gradlew --stacktrace ...` when you need Gradle stack traces.",
		"- `build-brief` normalizes output-shaping flags like `--quiet`, `--warn`, `--warning-mode ...`, and `--console ...` so its reducer keeps working reliably.",
		"- Let Gradle daemon reuse happen by default; `build-brief` strips explicit `--daemon` and `--no-daemon` overrides rather than forcing daemon-off behavior.",
		"- Preserve the raw log path from `build-brief` output when handing build failures to another tool or agent.",
	}

	if rtkInstalled {
		lines = append(lines,
			"- RTK is installed on this machine. Prefer `build-brief` directly for Gradle commands instead of wrapping Gradle work in RTK first.",
			"- If hooks or plugins rewrite raw `gradle` / `./gradlew` commands for you, let them route those commands — including chained `&&`, `||`, and `;` segments — to `build-brief` rather than sending Gradle through RTK first.",
		)
	}

	lines = append(lines, blockEnd)
	return strings.Join(lines, "\n")
}

func globalInstructions(tool Tool, rtkInstalled bool) string {
	lines := []string{
		blockStart,
		"## build-brief",
		"",
		"- Prefer `build-brief gradle ...` for PATH Gradle and `build-brief ./gradlew ...` for the project wrapper.",
		"- For chained shell commands, rewrite each Gradle segment individually, for example `build-brief gradle test && build-brief gradle check`.",
		"- Use the default `build-brief` output for routine Gradle work; clean success cases stay intentionally short.",
		"- Use `build-brief gradle --stacktrace ...` or `build-brief ./gradlew --stacktrace ...` when you need Gradle stack traces.",
		"- `build-brief` normalizes output-shaping flags like `--quiet`, `--warn`, `--warning-mode ...`, and `--console ...` so its reducer keeps working reliably.",
		"- Let Gradle daemon reuse happen by default; `build-brief` strips explicit `--daemon` and `--no-daemon` overrides instead of forcing daemon-off behavior.",
		"- Keep the raw log path in your response when build diagnostics may need deeper inspection.",
	}

	if rtkInstalled {
		lines = append(lines,
			"- RTK is installed on this machine. Prefer `build-brief` directly for Gradle commands instead of wrapping Gradle work in RTK first.",
			"- If this tool rewrites or intercepts raw Gradle commands, let that path route chained `&&`, `||`, and `;` Gradle segments to `build-brief` too instead of sending Gradle through RTK first.",
		)
	}

	if tool.SupportsHookGuides {
		lines = append(lines,
			"",
			"### Hook guidance",
			"",
			"- If this tool supports command hooks, add a guardrail that reminds or rewrites routine `gradle`/`./gradlew` usage to `build-brief gradle ...` or `build-brief ./gradlew ...` where safe, including chained `&&`, `||`, and `;` shell segments.",
			"- Keep raw Gradle available as a fallback for unusual cases or when full unfiltered output is explicitly needed.",
		)
	}

	if tool.SupportsPlugin {
		lines = append(lines,
			"",
			"### Plugin guidance",
			"",
		)
		lines = append(lines, pluginGuidance(tool)...)
	}

	lines = append(lines, blockEnd)
	return strings.Join(lines, "\n")
}

func pluginGuidance(tool Tool) []string {
	switch tool.ID {
	case "copilot-cli":
		return []string{
			"- The managed GitHub Copilot CLI plugin installs a local plugin bundle and registers it with `copilot plugin install`.",
			"- Its `preToolUse` hook blocks routine raw `gradle` and `./gradlew` Bash commands, including chained `&&`, `||`, and `;` Gradle segments, and suggests the `build-brief rewrite ...` result instead.",
			"- GitHub Copilot CLI hooks act as a guardrail here rather than an in-place command rewriter, so keep raw Gradle available for intentional bypasses.",
		}
	case "claude-code":
		return []string{
			"- The managed Claude Code plugin installs a local marketplace and registers a local `build-brief` plugin with `claude plugin install`.",
			"- Its `PreToolUse` hook blocks routine raw `gradle` and `./gradlew` Bash commands, including chained `&&`, `||`, and `;` Gradle segments, and suggests the `build-brief rewrite ...` result instead.",
			"- Claude Code hooks provide the blocking guardrail, not an in-place command rewrite, so keep raw Gradle available when you intentionally want to bypass it.",
		}
	case "codex-cli":
		return []string{
			"- The managed Codex plugin installs a local plugin bundle and enables Codex `PreToolUse` hooks for `build-brief`.",
			"- Its Bash hook blocks routine raw `gradle` and `./gradlew` commands, including chained `&&`, `||`, and `;` Gradle segments, and suggests the `build-brief rewrite ...` result instead.",
			"- Codex hooks cannot transparently rewrite and continue in place today, so keep raw Gradle available when you intentionally want to bypass that guardrail.",
		}
	default:
		return []string{
			"- The managed OpenCode plugin rewrites routine `gradle` and `./gradlew` shell commands — including chained `&&`, `||`, and `;` Gradle segments — to explicit `build-brief gradle ...` or `build-brief ./gradlew ...` commands before execution.",
			"- Keep using raw Gradle intentionally only when you want to bypass that reduction layer.",
		}
	}
}

func installOpenCodePlugin(tool DetectedTool) (string, error) {
	baseDir := filepath.Dir(tool.PreferredTarget)
	if baseDir == "." || baseDir == "" {
		home, err := userHomeDir()
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

func installClaudePlugin(tool DetectedTool) (string, error) {
	if strings.TrimSpace(tool.DetectedBinary) == "" {
		return "", errors.New("claude binary not found in PATH")
	}

	home, err := userHomeDir()
	if err != nil {
		return "", err
	}

	marketplaceRoot := filepath.Join(home, ".claude", "marketplaces", "build-brief")
	pluginRoot := filepath.Join(marketplaceRoot, "plugins", "build-brief")

	if err := writeJSONFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), claudePluginManifest()); err != nil {
		return "", err
	}
	if err := writeJSONFile(filepath.Join(pluginRoot, "hooks", "hooks.json"), claudePluginHooksConfig()); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "scripts"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "scripts", "pretooluse-build-brief.sh"), []byte(claudePluginPreToolUseScript()), 0o755); err != nil {
		return "", err
	}

	if err := writeJSONFile(filepath.Join(marketplaceRoot, ".claude-plugin", "marketplace.json"), claudePluginMarketplace()); err != nil {
		return "", err
	}

	if err := runClaudePluginInstall(tool.DetectedBinary, marketplaceRoot, claudePluginRef()); err != nil {
		return "", err
	}

	return pluginRoot, nil
}

func installCopilotPlugin(tool DetectedTool) (string, error) {
	if strings.TrimSpace(tool.DetectedBinary) == "" {
		return "", errors.New("copilot binary not found in PATH")
	}

	home, err := userHomeDir()
	if err != nil {
		return "", err
	}

	sourceDir := filepath.Join(home, ".copilot", "plugins", "build-brief")
	if err := writeJSONFile(filepath.Join(sourceDir, "plugin.json"), copilotPluginManifest()); err != nil {
		return "", err
	}
	if err := writeJSONFile(filepath.Join(sourceDir, "hooks.json"), copilotHooksConfig()); err != nil {
		return "", err
	}

	if err := runCopilotPluginInstall(tool.DetectedBinary, sourceDir); err != nil {
		return "", err
	}

	return sourceDir, nil
}

func installCodexPlugin(tool DetectedTool) (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}

	baseDir := filepath.Dir(tool.PreferredTarget)
	if baseDir == "." || baseDir == "" {
		baseDir = filepath.Join(home, ".codex")
	}

	sourceDir := filepath.Join(baseDir, "plugins", codexPluginName)
	if err := writeJSONFile(filepath.Join(sourceDir, ".codex-plugin", "plugin.json"), codexPluginManifest()); err != nil {
		return "", err
	}
	if err := writeJSONFile(filepath.Join(sourceDir, "hooks.json"), codexHooksConfig()); err != nil {
		return "", err
	}

	marketplacePath := filepath.Join(home, ".agents", "plugins", "marketplace.json")
	if err := upsertCodexMarketplace(marketplacePath, codexMarketplaceSourcePath()); err != nil {
		return "", err
	}

	cacheDir := filepath.Join(baseDir, "plugins", "cache", codexMarketplaceName, codexPluginName, "local")
	if err := os.RemoveAll(cacheDir); err != nil {
		return "", err
	}
	if err := copyDir(sourceDir, cacheDir); err != nil {
		return "", err
	}

	configPath := filepath.Join(baseDir, "config.toml")
	if err := upsertTOMLBool(configPath, "[features]", "hooks", true); err != nil {
		return "", err
	}
	if err := upsertTOMLBool(configPath, fmt.Sprintf(`[plugins.%q]`, codexPluginID()), "enabled", true); err != nil {
		return "", err
	}

	return sourceDir, nil
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

const (
	codexMarketplaceName        = "local-user-plugins"
	codexMarketplaceDisplayName = "Local User Plugins"
	codexPluginName             = "build-brief"
)

func codexPluginID() string {
	return codexPluginName + "@" + codexMarketplaceName
}

func codexMarketplaceSourcePath() string {
	return "./" + filepath.ToSlash(filepath.Join(".codex", "plugins", codexPluginName))
}

func copilotPluginManifest() map[string]any {
	return map[string]any{
		"name":        "build-brief",
		"description": "Guard routine Gradle Bash commands by steering GitHub Copilot CLI toward build-brief.",
		"version":     "1.0.0",
		"author": map[string]any{
			"name": "Static Var",
			"url":  "https://bb.staticvar.dev",
		},
		"license":  "MIT",
		"keywords": []string{"gradle", "build-brief", "copilot"},
		"hooks":    "hooks.json",
	}
}

func copilotHooksConfig() map[string]any {
	return map[string]any{
		"version": 1,
		"hooks": map[string]any{
			"preToolUse": []any{
				map[string]any{
					"type":       "command",
					"bash":       copilotPreToolUseCommand(),
					"timeoutSec": 30,
				},
			},
		},
	}
}

func copilotPreToolUseCommand() string {
	return `python3 -c "
import json
import subprocess
import sys

try:
    payload = json.load(sys.stdin)
except Exception:
    raise SystemExit(0)

if not isinstance(payload, dict):
    raise SystemExit(0)

tool_name = str(payload.get('toolName', '')).lower()
if tool_name != 'bash':
    raise SystemExit(0)

tool_args = payload.get('toolArgs')
if not isinstance(tool_args, str) or not tool_args.strip():
    raise SystemExit(0)

try:
    parsed_args = json.loads(tool_args)
except Exception:
    raise SystemExit(0)

if not isinstance(parsed_args, dict):
    raise SystemExit(0)

command = parsed_args.get('command', '')
if not isinstance(command, str) or not command.strip():
    raise SystemExit(0)
if 'build-brief' in command:
    raise SystemExit(0)

try:
    result = subprocess.run(
        ['build-brief', 'rewrite', command],
        check=False,
        capture_output=True,
        text=True,
    )
except FileNotFoundError:
    raise SystemExit(0)

rewritten = result.stdout.strip()
if result.returncode != 0 or not rewritten or rewritten == command.strip():
    raise SystemExit(0)

json.dump(
    {
        'permissionDecision': 'deny',
        'permissionDecisionReason': 'Routine Gradle command or chain intercepted by build-brief. Use: ' + rewritten,
    },
    sys.stdout,
    separators=(',', ':'),
)
"`
}

const (
	claudeMarketplaceName = "build-brief-local"
	claudePluginName      = "build-brief"
)

func claudePluginRef() string {
	return claudePluginName + "@" + claudeMarketplaceName
}

func claudePluginManifest() map[string]any {
	return map[string]any{
		"name":        claudePluginName,
		"version":     "1.0.0",
		"description": "Guard routine Gradle Bash commands by steering Claude Code toward build-brief.",
		"author": map[string]any{
			"name": "Static Var",
			"url":  "https://bb.staticvar.dev",
		},
		"homepage":   "https://bb.staticvar.dev",
		"repository": "https://github.com/static-var/build-brief",
		"license":    "MIT",
		"keywords":   []string{"gradle", "build-brief", "claude-code"},
	}
}

func claudePluginMarketplace() map[string]any {
	return map[string]any{
		"name": claudeMarketplaceName,
		"owner": map[string]any{
			"name": "Static Var",
		},
		"plugins": []any{
			map[string]any{
				"name":        claudePluginName,
				"source":      "./plugins/build-brief",
				"description": "Guard routine Gradle Bash commands by steering Claude Code toward build-brief.",
			},
		},
	}
}

func claudePluginHooksConfig() map[string]any {
	return map[string]any{
		"description": "build-brief guardrail hooks",
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{
							"type":          "command",
							"command":       `"${CLAUDE_PLUGIN_ROOT}/scripts/pretooluse-build-brief.sh"`,
							"timeout":       30,
							"statusMessage": "Checking Bash command for build-brief",
						},
					},
				},
			},
		},
	}
}

func claudePluginPreToolUseScript() string {
	return `#!/usr/bin/env bash

set -euo pipefail

if ! command -v build-brief >/dev/null 2>&1; then
  exit 0
fi

payload="$(cat)"
if [[ -z "$payload" ]]; then
  exit 0
fi

original_command="$(python3 - "$payload" <<'PY'
import json
import sys

try:
    payload = json.loads(sys.argv[1])
except Exception:
    print("")
    raise SystemExit(0)

if not isinstance(payload, dict):
    print("")
    raise SystemExit(0)

tool_name = payload.get("tool_name", "")
if tool_name != "Bash":
    print("")
    raise SystemExit(0)

tool_input = payload.get("tool_input", {})
if not isinstance(tool_input, dict):
    print("")
    raise SystemExit(0)

command = tool_input.get("command", "")
print(command if isinstance(command, str) else "")
PY
)"

if [[ -z "$original_command" || "$original_command" == *"build-brief"* ]]; then
  exit 0
fi

rewritten_command="$(build-brief rewrite "$original_command" 2>/dev/null || true)"
if [[ -z "$rewritten_command" || "$rewritten_command" == "$original_command" ]]; then
  exit 0
fi

python3 - "$rewritten_command" <<'PY'
import json
import sys

rewritten = sys.argv[1]
json.dump(
    {
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "deny",
            "permissionDecisionReason": f"Routine Gradle command or chain intercepted by build-brief. Use: {rewritten}",
        }
    },
    sys.stdout,
    separators=(",", ":"),
)
PY
`
}

func codexPluginManifest() map[string]any {
	return map[string]any{
		"name":        codexPluginName,
		"version":     "1.0.0",
		"description": "Guard routine Gradle Bash commands by steering Codex toward build-brief.",
		"author": map[string]any{
			"name": "Static Var",
			"url":  "https://bb.staticvar.dev",
		},
		"homepage":   "https://bb.staticvar.dev",
		"repository": "https://github.com/static-var/build-brief",
		"license":    "MIT",
		"keywords":   []string{"gradle", "build-brief", "codex"},
		"hooks":      "./hooks.json",
		"interface": map[string]any{
			"displayName":      "build-brief",
			"shortDescription": "Steer routine Gradle work through build-brief.",
			"longDescription":  "Blocks routine raw Gradle Bash commands in Codex and suggests the equivalent build-brief command instead.",
			"developerName":    "Static Var",
			"category":         "Productivity",
			"websiteURL":       "https://bb.staticvar.dev",
			"defaultPrompt": []string{
				"Use build-brief for this Gradle task.",
				"Prefer build-brief over raw gradle for routine builds.",
			},
			"brandColor": "#0f172a",
		},
	}
}

func codexHooksConfig() map[string]any {
	return map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{
							"type":          "command",
							"statusMessage": "Checking Bash command for build-brief",
							"command":       codexPreToolUseCommand(),
						},
					},
				},
			},
		},
	}
}

func codexPreToolUseCommand() string {
	return `python3 -c "
import json
import subprocess
import sys

try:
    payload = json.load(sys.stdin)
except Exception:
    raise SystemExit(0)

if not isinstance(payload, dict):
    raise SystemExit(0)

tool_input = payload.get('tool_input')
if not isinstance(tool_input, dict):
    raise SystemExit(0)

command = tool_input.get('command', '')
if not isinstance(command, str) or not command.strip():
    raise SystemExit(0)
if 'build-brief' in command:
    raise SystemExit(0)

try:
    result = subprocess.run(
        ['build-brief', 'rewrite', command],
        check=False,
        capture_output=True,
        text=True,
    )
except FileNotFoundError:
    raise SystemExit(0)

rewritten = result.stdout.strip()
if result.returncode != 0 or not rewritten or rewritten == command.strip():
    raise SystemExit(0)

sys.stderr.write(
    '[build-brief] Codex blocked a routine Gradle command or chain.\n'
    '[build-brief] Use this instead:\n\n'
    '  ' + rewritten + '\n\n'
    'This Codex PreToolUse hook can block and suggest a safer replacement,\n'
    'but it cannot transparently rewrite and continue in place.\n'
)
raise SystemExit(2)
"`
}

func upsertCodexMarketplace(path, sourcePath string) error {
	marketplace := map[string]any{}
	if fileExists(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &marketplace); err != nil {
			return err
		}
	}

	if name, _ := marketplace["name"].(string); strings.TrimSpace(name) == "" {
		marketplace["name"] = codexMarketplaceName
	}

	iface, ok := marketplace["interface"].(map[string]any)
	if !ok || iface == nil {
		iface = map[string]any{}
	}
	if displayName, _ := iface["displayName"].(string); strings.TrimSpace(displayName) == "" {
		iface["displayName"] = codexMarketplaceDisplayName
	}
	marketplace["interface"] = iface

	plugins, ok := marketplace["plugins"].([]any)
	if !ok || plugins == nil {
		plugins = []any{}
	}

	entry := map[string]any{
		"name": codexPluginName,
		"source": map[string]any{
			"source": "local",
			"path":   sourcePath,
		},
		"policy": map[string]any{
			"installation":   "AVAILABLE",
			"authentication": "ON_INSTALL",
		},
		"category": "Productivity",
	}

	replaced := false
	for i, raw := range plugins {
		plugin, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := plugin["name"].(string); name == codexPluginName {
			plugins[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		plugins = append(plugins, entry)
	}
	marketplace["plugins"] = plugins

	return writeJSONFile(path, marketplace)
}

func upsertTOMLBool(path, section, key string, value bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	content := ""
	if fileExists(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content = string(data)
	}

	lines := strings.Split(content, "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}

	sectionStart := -1
	sectionEnd := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == section {
			sectionStart = i
			continue
		}
		if sectionStart >= 0 && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sectionEnd = i
			break
		}
	}

	entry := fmt.Sprintf("%s = %t", key, value)
	if sectionStart >= 0 {
		for i := sectionStart + 1; i < sectionEnd; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=") {
				lines[i] = entry
				return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
			}
		}

		insert := append([]string{}, lines[:sectionEnd]...)
		insert = append(insert, entry)
		insert = append(insert, lines[sectionEnd:]...)
		return os.WriteFile(path, []byte(strings.Join(insert, "\n")), 0o644)
	}

	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		lines = append(lines, "")
	}
	lines = append(lines, section, entry)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return err
		}
	}

	return nil
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
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
