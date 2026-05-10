# Hook guidance

Hooks are useful as optional adoption helpers, but they are still secondary to the
direct `build-brief` executable and instruction-file guidance.

Recommended use of hooks:

- remind agents to prefer `build-brief` over raw `gradle`
- archive raw log locations after command completion
- intercept routine Gradle shell commands before execution when the host supports it, including chained `&&`, `||`, and `;` segments

Why hooks are secondary:

- hook APIs vary across CLIs
- some hosts can rewrite commands in place, others can only block and suggest
- a direct executable remains the most portable integration surface

## OpenCode

OpenCode is the managed transparent-rewrite integration. `build-brief --global` writes a thin
plugin that uses OpenCode's `tool.execute.before` hook and delegates rewrite
decisions to:

```bash
build-brief rewrite "<original shell command>"
```

That plugin can transparently replace routine Gradle commands before execution,
including chained commands such as `gradle test && gradle check`.

## GitHub Copilot CLI

GitHub Copilot CLI now has official plugin packaging plus hook support. The
managed `build-brief --global` integration installs a local Copilot plugin and
registers it with:

```bash
copilot plugin install <local-plugin-path>
```

That plugin uses a `preToolUse` hook to inspect Bash commands, block routine raw
Gradle usage including chained segments such as `gradle test && gradle check`,
and suggest the `build-brief rewrite ...` result instead.

Like Codex and Claude Code, this is a guardrail rather than an in-place command
rewriter, so the plugin blocks and suggests instead of mutating the original
command and continuing automatically.

## Claude Code

Claude Code now has an official plugin system, so `build-brief --global` can
install a managed local plugin for Claude instead of only relying on a manual
hook setup.

That managed plugin uses Claude Code `PreToolUse` hooks under the hood. The
runtime behavior is still different from OpenCode: it can block a Bash command
and suggest the replacement, but it cannot transparently mutate the command and
continue in place the way the OpenCode plugin can.

Example files live here:

- `examples/hooks/claude-code/pretooluse-build-brief.sh`
- `examples/hooks/claude-code/settings.json`

Recommended setup:

1. copy the script to `.claude/hooks/pretooluse-build-brief.sh`
2. make it executable
3. merge the `PreToolUse` snippet from `examples/hooks/claude-code/settings.json`
   into your Claude Code settings

The example hook and the managed Claude plugin both block routine raw Gradle
commands, including chained segments such as `gradle test && gradle check`, and
suggest the `build-brief rewrite ...` result instead.

## Codex App & CLI

Codex App and Codex CLI share the `~/.codex` configuration/plugin area. Codex now has official hooks and plugin packaging, but the runtime constraint is
similar to Claude Code for this use case: `PreToolUse` can block a Bash command
and explain the replacement, but it cannot rewrite the command in place and
continue automatically.

The managed `build-brief --global` Codex App & CLI integration therefore installs:

- a local Codex plugin bundle under `~/.codex/plugins/build-brief` with `hooks.json`
- a local marketplace entry under `~/.agents/plugins/marketplace.json`
- a cached installed copy under the Codex plugin cache
- `config.toml` updates that enable `hooks` and turn the plugin on

That managed hook blocks routine raw Gradle commands, including chained segments
such as `gradle test && gradle check`, and suggests the `build-brief rewrite ...`
result instead.

## Pi Coding Agent

Pi extensions can mutate Bash tool input before execution, so the managed
`build-brief --global` Pi integration installs a local extension at:

```text
~/.pi/agent/extensions/build-brief/index.ts
```

The extension listens for `tool_call` events on the Bash tool, delegates command
analysis to `build-brief rewrite`, and replaces routine raw Gradle commands —
including chained `&&`, `||`, and `;` Gradle segments — with the equivalent
`build-brief gradle ...` or `build-brief ./gradlew ...` command before Pi runs
Bash.
