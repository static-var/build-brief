# Hook guidance

Hooks are useful as optional adoption helpers, but they should not be the primary implementation surface for `build-brief`.

Recommended use of hooks:

- inject aliases or helper functions at session start
- remind agents to prefer `build-brief` over raw `gradle`
- archive raw log locations after command completion

Why hooks are secondary:

- hook APIs vary across CLIs
- many hook systems cannot reliably rewrite every command invocation
- a direct executable remains the most portable integration surface

For tools that support repository instructions, prefer pairing `build-brief` with a custom-instructions file and a shell helper instead of relying on hooks alone.
