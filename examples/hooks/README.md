# Hook guidance

Hooks are useful as optional adoption helpers, but they should not be the primary implementation surface for `build-brief`.

Recommended use of hooks:

- inject aliases or helper functions at session start
- remind agents to prefer `build-brief` over raw `gradle`
- archive raw log locations after command completion
- rewrite routine Gradle shell commands to `build-brief` before execution when the host supports it

Why hooks are secondary:

- hook APIs vary across CLIs
- many hook systems cannot reliably rewrite every command invocation
- a direct executable remains the most portable integration surface

OpenCode is the current exception worth calling out. In local validation, global instructions alone were not enough to make OpenCode consistently choose `build-brief`. The practical fix was a managed plugin that uses OpenCode's `tool.execute.before` hook and delegates rewrite decisions to:

```bash
build-brief rewrite "<original shell command>"
```

That keeps the rewrite rules in one place and makes the OpenCode plugin intentionally thin.
