# build-brief for Codex

`build-brief` keeps Gradle output concise for coding agents while preserving the full raw Gradle log on disk.

This Codex plugin adds a `PreToolUse` Bash guardrail. When Codex tries to run a routine raw Gradle command such as:

```bash
gradle test
./gradlew assembleDebug
gradle test && gradle check
```

it delegates command analysis to:

```bash
build-brief rewrite <command>
```

and blocks the raw command with a suggested replacement such as:

```bash
build-brief gradle test
build-brief ./gradlew assembleDebug
build-brief gradle test && build-brief gradle check
```

Codex hooks can block and suggest the safer replacement, but they cannot transparently rewrite and continue the Bash command in place today.

## Requirement

Install the `build-brief` CLI first and make sure it is on `PATH`:

```bash
brew install static-var/tap/build-brief
```

Or build from source:

```bash
go build -o build-brief ./cmd/build-brief
```

## More

- Website: https://bb.staticvar.dev
- Repository: https://github.com/static-var/build-brief
- License: MIT
