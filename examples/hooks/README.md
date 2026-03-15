# Hook guidance

Hooks are useful as optional adoption helpers, but they are still secondary to the
direct `build-brief` executable and instruction-file guidance.

Recommended use of hooks:

- remind agents to prefer `build-brief` over raw `gradle`
- archive raw log locations after command completion
- intercept routine Gradle shell commands before execution when the host supports it

Why hooks are secondary:

- hook APIs vary across CLIs
- some hosts can rewrite commands in place, others can only block and suggest
- a direct executable remains the most portable integration surface

## OpenCode

OpenCode is the current managed integration. `build-brief --global` writes a thin
plugin that uses OpenCode's `tool.execute.before` hook and delegates rewrite
decisions to:

```bash
build-brief rewrite "<original shell command>"
```

That plugin can transparently replace routine Gradle commands before execution.

## Claude Code

Claude Code hooks are different. A `PreToolUse` hook can inspect a bash command and
block it with a suggested replacement, but it cannot transparently mutate the
command and continue in place the way the OpenCode plugin can.

Example files live here:

- `examples/hooks/claude-code/pretooluse-build-brief.sh`
- `examples/hooks/claude-code/settings.json`

Recommended setup:

1. copy the script to `.claude/hooks/pretooluse-build-brief.sh`
2. make it executable
3. merge the `PreToolUse` snippet from `examples/hooks/claude-code/settings.json`
   into your Claude Code settings

The example hook blocks routine raw Gradle commands and suggests the
`build-brief rewrite ...` result instead.
