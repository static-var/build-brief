Use `build-brief` instead of invoking `./gradlew` or `gradle` directly when you want reduced output.

Preferred patterns:

- `build-brief test`
- `build-brief build`
- `build-brief -- --stacktrace test`

Behavior rules:

- Prefer `build-brief` for routine `build`, `test`, and `assemble` commands.
- Keep default `build-brief` output for routine work; clean success cases should stay short.
- Fall back to raw Gradle only when the reduced output is insufficient.
- Preserve the raw log path from `build-brief` output when handing diagnostics to another tool or agent.
