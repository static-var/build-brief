Use `build-brief gradle ...` for PATH Gradle and `build-brief ./gradlew ...` for a project wrapper when you want reduced output.

Preferred patterns:

- `build-brief gradle test`
- `build-brief ./gradlew build`
- `build-brief gradle --stacktrace test`

Behavior rules:

- Prefer explicit `build-brief gradle ...` or `build-brief ./gradlew ...` forms for routine `build`, `test`, and `assemble` commands.
- Keep default `build-brief` output for routine work; clean success cases should stay short.
- `build-brief` normalizes output-shaping flags like `--quiet`, `--warn`, `--silent`, `--warning-mode ...`, and `--console ...` for you.
- Let Gradle daemon reuse happen by default; `build-brief` strips explicit `--daemon` and `--no-daemon` overrides.
- Fall back to raw Gradle only when the reduced output is insufficient.
- Preserve the raw log path from `build-brief` output when handing diagnostics to another tool or agent.
