Use `build-brief` instead of invoking `./gradlew` or `gradle` directly when you want reduced output.

Preferred patterns:

- `build-brief test`
- `build-brief --mode json build`
- `build-brief -- --stacktrace test`

Behavior rules:

- Prefer `build-brief` for routine `build`, `test`, and `assemble` commands.
- Use `--mode json` when another tool needs deterministic machine-readable output.
- Fall back to raw Gradle only when the reduced output is insufficient.
- Preserve the raw log path from `build-brief` output when handing diagnostics to another tool or agent.
