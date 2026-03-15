# build-brief

`build-brief` is a small Go CLI that sits in front of Gradle, keeps the full raw log on disk, and cuts terminal output down to the parts that usually matter.

It is inspired by [`rtk`](https://github.com/rtk-ai/rtk), but RTK is not compatible enough with Gradle workflows to reuse directly here, so `build-brief` applies the same output-reduction idea specifically to Gradle builds.

Project site: <https://bb.staticvar.dev>

## what it does

- resolves Gradle in this order: explicit `--gradle` path, explicit invocation like `build-brief gradle ...` or `build-brief ./gradlew ...`, project-local `./gradlew`, then system `gradle`
- normalizes output-shaping flags like `--console ...`, `--warning-mode ...`, `--quiet`, and `--warn` so the reducer keeps seeing stable parseable output
- accepts either the original `gradle` command or `./gradlew` explicitly when you want to preserve that choice
- leaves Gradle daemon reuse enabled by default and strips explicit `--daemon` / `--no-daemon` overrides
- preserves the Gradle exit code
- writes the raw log to a reusable per-project `latest` log file
- summarizes failed tasks, failed tests, warnings, generated bundle artifacts, omitted compilation-output counts, and the final build status
- supports concise default output plus `raw` replay mode
- keeps rough token-savings history with `build-brief gains`
- can install agent instructions locally and into selected global agent instruction files

## real output examples

Failure summary with assertion mismatch:

```text
$ build-brief gradle test --tests example.FailingTest
BUILD FAILED in 564ms
Command: gradle --console=plain test --tests example.FailingTest
Failed tasks:
  - :test
Failed tests:
  - FailingTest > intentionalFailure()
Highlights:
  - FailingTest > intentionalFailure(): org.opentest4j.AssertionFailedError: expected: <expected> but was: <hello, build-brief>
  - at example.FailingTest.intentionalFailure(FailingTest.java:10)
Raw log: /tmp/build-brief/...latest.log
```

Warm successful Android build with APK path:

```text
$ build-brief ./gradlew :androidApp:assembleDebug
BUILD SUCCESSFUL in 3s
Artifacts:
  - APK: androidApp/build/outputs/apk/debug/androidApp-debug.apk (24.5 MB)
```

## installation

Choose whichever install path fits:

### build from source

`go.mod` currently declares Go `1.26.1`, so use a matching Go toolchain.

```bash
go build -o build-brief ./cmd/build-brief
```

Then put the binary on your `PATH`.

On macOS or Linux:

```bash
mv build-brief /usr/local/bin/
```

On Windows, build `build-brief.exe` and place it in a directory that is already on `PATH`.

### install with a shell script

For macOS and Linux, there is also a pipe-to-bash installer that resolves the exact matching asset from GitHub release metadata, verifies its checksum, and installs `build-brief` into a writable bin directory.

```bash
curl -fsSL https://bb.staticvar.dev/install.sh | bash
wget -qO- https://bb.staticvar.dev/install.sh | bash
```

You can pass installer options through `bash -s --`, for example:

```bash
curl -fsSL https://bb.staticvar.dev/install.sh | bash -s -- --bin-dir /usr/local/bin
curl -fsSL https://bb.staticvar.dev/install.sh | bash -s -- --version 0.1.0
```

The script currently targets Unix-like systems. Windows should keep using the release archives or build-from-source path.

### install with Homebrew or Linuxbrew

On macOS and Linux:

```bash
brew tap static-var/tap
brew install static-var/tap/build-brief
```

Releases publish archives and checksums for:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`
- `windows/arm64`

## quick start

```bash
build-brief test
build-brief build
build-brief gradle test
build-brief ./gradlew test
build-brief --gradle-user-home /tmp/build-brief-gradle-home ./gradlew test
build-brief --project-dir /path/to/project test
build-brief gains --history
build-brief -- --stacktrace test
```

If you want to keep the original command shape explicit, prefer `build-brief gradle ...` for a PATH-resolved Gradle binary and `build-brief ./gradlew ...` for a project-local wrapper.

If you want to pass Gradle flags that look like `build-brief` flags, use `--` to separate them.

For the full command reference, run:

```bash
build-brief --help
```

## output modes

- default: concise Gradle summary, with especially short output on clean success cases and standard packaged outputs like APK/AAB/AAR/JAR/WAR/ZIP plus KMP artifacts such as frameworks, XCFrameworks, KLIBs, and KEXEs when they were generated or are still available for the targeted artifact-producing task
- `raw`: replay the captured Gradle log without reduction

For standard Gradle output locations, `build-brief` snapshots known artifact roots before the build, scans them again afterward, and reports new or changed outputs first.

If that finds nothing for a successful artifact-producing task such as `assemble` or `bundle`, it can fall back to already-available artifacts under the targeted Gradle project path so warm runs can still show the expected APK or bundle path.

Verified artifact paths printed in logs are also used when the file or bundle really exists.

## daemon reuse

`build-brief` now lets Gradle reuse an existing daemon or start one when needed. It strips explicit `--daemon` and `--no-daemon` overrides so the reducer can keep one consistent execution policy.

If you want repeated agent or script runs to reuse caches and daemon state more reliably, use a shared Gradle user home:

```bash
build-brief --gradle-user-home /tmp/build-brief-gradle-home ./gradlew test
```

That matters most when a CLI or agent is running a loop of Gradle commands in separate subprocesses and you want stable caches and daemon reuse across runs.

## raw log behavior

`build-brief` does not keep the full Gradle log in memory.

Instead, it streams output straight to disk and reuses one per-project `latest` log file under the system temp directory. That keeps memory use steady and still leaves you with the full log when the short summary is not enough.

On failures, the concise summary prints the raw log path directly. On long-running builds, `build-brief` also emits periodic stderr heartbeats with the raw log path. Clean successful summaries stay shorter, but the log is still retained on disk and available through `raw` mode.

## gains and token estimates

`build-brief gains` shows rough token savings based on a simple chars-divided-by-4 estimate.

```bash
build-brief gains
build-brief gains --history
build-brief gains --format json
build-brief gains --reset
```

It is meant for rough savings/accounting feedback, not billing-grade accounting, and it stays intentionally token-focused rather than time-focused.

## supported Gradle projects

This tool works at the Gradle process and log level, so it is not tied to one plugin stack.

That means it is meant to work across:

- Spring Boot
- Ktor
- Android
- Kotlin Multiplatform
- plain JVM projects
- multi-project Gradle builds

## current limitations

- Artifact reporting is strongest for standard Gradle output locations plus verified artifact paths that appear in logs. Custom output directories or unusual plugin layouts may not always be detected correctly.
- The command-rewrite and agent-install flows target common Gradle command shapes and known tool integrations. Unusual shell constructs, custom hooks, or unsupported agent tools may bypass that path.
- Clean successful runs intentionally stay very short, so some details are only available through the retained raw log or `raw` mode.
- Platform support exists for macOS, Linux, and Windows, but the project has not been heavily tested across every OS, shell, CI environment, and Gradle/plugin combination yet.

Expect some bugs and rough edges, especially on less-common setups. If something looks wrong, keep the raw log, rerun in `raw` mode if needed, and treat that as the fallback source of truth.

## installing agent instructions

`build-brief` has two install flows.

### local install

Use this when you want a project-level instruction block in `AGENTS.md`.

```bash
build-brief --install
build-brief --install-force
```

- `--install` updates `AGENTS.md` in the current directory
- `--install-force` does the same thing, but creates `AGENTS.md` first if it does not exist

### global install

Use this when you want to update existing global instruction files for supported AI tools.

```bash
build-brief --global
```

`--global` is interactive. It checks known binaries and instruction files, shows a numbered list of detected tools, updates only existing global instruction files, and also writes a managed OpenCode plugin file when OpenCode is selected.

## supported global agent targets

Today the registry covers these tools:

| Tool | Detection binary | Global instruction targets |
| --- | --- | --- |
| GitHub Copilot CLI | `copilot` | `~/.copilot/copilot-instructions.md` |
| Claude Code | `claude` | `~/.claude/CLAUDE.md` |
| Codex CLI | `codex` | `~/.codex/AGENTS.md`, `os.UserConfigDir()/codex/AGENTS.md` |
| OpenCode | `opencode` | `~/.config/opencode/AGENTS.md`, `~/.opencode/AGENTS.md` |
| Gemini CLI | `gemini` | `~/.gemini/GEMINI.md` |

For OpenCode, `--global` also writes `plugins/build-brief.ts` next to the selected config root, and that plugin delegates rewrite decisions to `build-brief rewrite`.

## other agentic AI tools

If your tool supports `AGENTS.md`, an instructions file, or something similar, drop in a short block like this:

```md
Use `build-brief` for routine Gradle commands.

- Prefer `build-brief gradle ...` for a PATH-resolved Gradle binary.
- Prefer `build-brief ./gradlew ...` when the repo has a Gradle wrapper.
- Let `build-brief` keep `--console=plain` and normalize output-shaping flags.
- Do not add `--daemon` or `--no-daemon`; let normal daemon reuse happen.
- Fall back to raw Gradle only when the reduced summary is not enough.
```

That gives unsupported tools a simple, portable default without depending on a custom plugin API.

## Claude Code hook example

Claude Code hooks do not behave the same way as the managed OpenCode plugin.

- OpenCode can transparently rewrite a command before execution.
- Claude Code `PreToolUse` hooks can inspect a bash command, block it, and suggest the rewritten command, but they cannot transparently mutate the command and continue in place.

Example files:

- `examples/hooks/claude-code/pretooluse-build-brief.sh`
- `examples/hooks/claude-code/settings.json`

Typical setup:

```bash
mkdir -p .claude/hooks
cp examples/hooks/claude-code/pretooluse-build-brief.sh .claude/hooks/
chmod +x .claude/hooks/pretooluse-build-brief.sh
```

Then merge the `PreToolUse` snippet from `examples/hooks/claude-code/settings.json` into your Claude Code settings file.

## rewrite command

`build-brief rewrite` exists for hooks and plugins.

```bash
build-brief rewrite "gradle clean"
build-brief rewrite "./gradlew --stacktrace test"
```

Examples:

- `gradle clean` -> `build-brief gradle clean`
- `./gradlew test` -> `build-brief ./gradlew test`
- `which gradle && gradle clean` -> `command -v build-brief && build-brief gradle clean`

The idea is to keep rewrite rules in one place inside `build-brief` and let thin integrations call into them.

## environment variables

- `BUILD_BRIEF_MODE`
- `BUILD_BRIEF_PROJECT_DIR`
- `BUILD_BRIEF_GRADLE_PATH`
- `BUILD_BRIEF_GRADLE_USER_HOME`
- `BUILD_BRIEF_LOG_DIR`

## examples

- `examples/instructions/agent-instructions.md`
- `examples/shell/build-brief.zsh`
- `examples/hooks/README.md`

## smoke projects and agent validation

The repo includes a small smoke matrix under `smoke/projects/`:

- `jvm-junit`
- `springboot-junit`
- `ktor-kotlin-test`
- `kmp-library`
- `android-app`

There is also a harness at `smoke/run-agent-smoke.sh` that runs Codex and OpenCode against those projects without telling them to use `build-brief` explicitly. Useful options:

- `--case <case-id>`
- `--timeout <seconds>`

Each harness run writes to its own timestamp-and-pid output directory under `smoke/out/` and updates `smoke/out/latest`.

Recent local validation on this machine:

- Codex used `build-brief` for JVM, Spring Boot, Ktor, and KMP prompts
- OpenCode instructions alone were not reliable enough on this machine
- after adding the managed OpenCode plugin, the same Spring Boot smoke prompt executed `build-brief clean` and `build-brief test`
- the Android smoke project is in place, but full Android test runs are still the slow lane and should be treated separately from the fast loop

## license

MIT
