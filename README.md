# build-brief

`build-brief` is a small Go CLI that sits in front of Gradle.

Gradle output gets noisy fast. That is manageable when you are running one build by hand. It gets old when a script, CLI, or coding agent is running builds in a loop and dumping all of that output into a terminal or context window.

`build-brief` keeps the full raw log on disk and cuts the terminal output down to the parts that usually matter.

It is inspired by [`rtk`](https://github.com/rtk-ai/rtk), but RTK is not compatible enough with Gradle workflows to reuse directly here, so `build-brief` applies the same output-reduction idea specifically to Gradle builds.

## what it does

- resolves Gradle in this order: explicit `--gradle` path, explicit invocation like `build-brief gradle ...` or `build-brief ./gradlew ...`, project-local `./gradlew`, then system `gradle`
- injects `--console=plain` unless you already set that exact console mode
- rejects flags that would suppress the signal it needs, including `--daemon`, `--quiet`, `-q`, `--warn`, `-w`, `--warning-mode none`, and non-plain `--console` values
- accepts either the original `gradle` command or `./gradlew` explicitly when you want to preserve that choice
- always runs Gradle with `--no-daemon` and can still share a Gradle user home across runs
- preserves the Gradle exit code
- writes the raw log to a reusable per-project `latest` log file
- summarizes failed tasks, failed tests, warnings, generated bundle artifacts, omitted compilation-output counts, and the final build status
- supports concise default output plus `raw` replay mode
- tracks rough token savings over time with `build-brief gains`
- can install agent instructions locally and into selected global agent instruction files

## installation

There are really two installation stories here: what works today, and how this should be shipped once releases exist.

### build from source today

Right now the simplest path is to build from a checked-out copy of the repo.

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

### install with Homebrew or Linuxbrew

Once the release workflow and tap repo are configured, users on macOS and Linux can install with:

```bash
brew tap static-var/tap
brew install static-var/tap/build-brief
```

The release workflow generates `Formula/build-brief.rb` from the exact GitHub release archives, then pushes it to a separate tap repository. The default target is `<repo-owner>/homebrew-tap`, and you can override it with the repository variable `HOMEBREW_TAP_REPOSITORY`.

To enable tap publishing from GitHub Actions:

1. create a public tap repo such as `owner/homebrew-tap`
2. add the repository secret `HOMEBREW_TAP_TOKEN` with permission to push to that repo
3. optionally set `HOMEBREW_TAP_REPOSITORY` and `HOMEBREW_TAP_BRANCH`

### how releases should work

If this project is published properly, the practical release model is one binary per OS and CPU architecture.

There is no single binary that will run everywhere. Go gives you easy cross-compilation, but the output is still platform-specific. In practice that means release artifacts like these:

- `build-brief-darwin-arm64`
- `build-brief-darwin-amd64`
- `build-brief-linux-amd64`
- `build-brief-linux-arm64`
- `build-brief-windows-amd64.exe`
- `build-brief-windows-arm64.exe`

This repo now includes a manual GitHub Actions release workflow on `ubuntu-latest` that bumps the version, updates `CHANGELOG.md`, commits the release, creates the tag, cross-compiles the binaries, and publishes both workflow artifacts and GitHub release assets.

That same workflow also generates a Homebrew formula from the release assets and, when `HOMEBREW_TAP_TOKEN` is configured, pushes `Formula/build-brief.rb` to the tap repo so `brew install` works on both macOS and Linux.

Each release publishes archived binaries and checksums for:

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

- default: concise Gradle summary, with especially short output on clean success cases and standard packaged outputs like APK/AAB/AAR/JAR/WAR/ZIP plus KMP artifacts such as frameworks, XCFrameworks, KLIBs, and KEXEs when they were generated
- `raw`: replay the captured Gradle log without reduction

For standard Gradle output locations, `build-brief` now uses a hybrid detector: it snapshots known artifact roots before the build, scans them again afterward, and reports only new or changed outputs.

If Gradle or a plugin prints an explicit artifact path in the log, `build-brief` can also use that as a verified hint, but it still checks that the file or bundle really exists and was updated during the current run before showing it.

Successful artifact reporting is only shown for successful runs today. Failed builds still focus on failures, tests, warnings, highlights, and the raw log path.

## daemon reuse

`build-brief` does not implement its own daemon, and it always adds `--no-daemon` to the Gradle invocation unless you already passed `--no-daemon` yourself.

That means it will not leave a Gradle daemon behind after each run.

If you try to force the opposite behavior with `--daemon`, `build-brief` rejects that flag instead of silently ignoring it.

If you want repeated agent or script runs to reuse caches more reliably, use a shared Gradle user home:

```bash
build-brief --gradle-user-home /tmp/build-brief-gradle-home ./gradlew test
```

That matters most when a CLI or agent is running a loop of Gradle commands in separate subprocesses and you still want a stable cache location even without daemon reuse.

## raw log behavior

`build-brief` does not keep the full Gradle log in memory.

Instead, it streams output straight to disk and reuses one per-project `latest` log file under the system temp directory. That keeps memory use steady and still leaves you with the full log when the short summary is not enough.

On failures, the concise summary prints the raw log path directly. On long-running builds, `build-brief` also emits periodic stderr heartbeats with the raw log path. Clean successful summaries stay shorter, but the log is still retained on disk and available through `raw` mode.

## gains and token tracking

`build-brief gains` shows rough token savings based on a simple chars-divided-by-4 estimate, which mirrors the kind of approximation RTK uses.

```bash
build-brief gains
build-brief gains --history
build-brief gains --format json
build-brief gains --reset
```

This is meant as operational feedback, not billing-grade accounting.

The gains view is intentionally token-focused. It does not report execution time or timing-based efficiency stats.

You will see some commands save a lot of tokens and some save none. The feature is most useful when agents are hitting noisy builds, tests, or failures repeatedly.

## supported Gradle projects

This tool works at the Gradle process and log level, so it is not tied to one plugin stack.

That means it is meant to work across:

- Spring Boot
- Ktor
- Android
- Kotlin Multiplatform
- plain JVM projects
- multi-project Gradle builds

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

How it works:

1. it checks for known binaries on `PATH`
2. it checks known global instruction file locations
3. it shows a numbered list of detected tools
4. you choose which tools to update
5. it updates only files that already exist
6. for OpenCode, it also writes a managed plugin file that rewrites routine Gradle shell commands to `build-brief`

That last part still matters. `--global` does not create missing global instruction files. The one exception is the managed OpenCode plugin file, because the plugin is now the reliable integration path there.

`--global` is interactive. It does not install into every detected tool automatically.

`--global` also has to stand on its own. `build-brief` rejects `--global --install` and `--global --install-force` so the local and global flows stay clear.

## supported global agent targets

Today the registry covers these tools:

| Tool | Detection binary | Global instruction targets |
| --- | --- | --- |
| GitHub Copilot CLI | `copilot` | `~/.copilot/copilot-instructions.md` |
| Claude Code | `claude` | `~/.claude/CLAUDE.md` |
| Codex CLI | `codex` | `~/.codex/AGENTS.md`, `os.UserConfigDir()/codex/AGENTS.md` |
| OpenCode | `opencode` | `~/.config/opencode/AGENTS.md`, `~/.opencode/AGENTS.md` |
| Gemini CLI | `gemini` | `~/.gemini/GEMINI.md` |

For tools with richer hook systems, `build-brief` can now go further than plain instruction text. For OpenCode, `--global` also writes `plugins/build-brief.ts` next to the selected OpenCode config root, and that plugin delegates rewrite decisions to `build-brief rewrite`.

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

The idea is simple: keep the rewrite rules in one place inside `build-brief`, then let thin integrations call into that logic instead of duplicating shell parsing in every plugin.

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

The repo now includes a small smoke matrix under `smoke/projects/`:

- `jvm-junit`
- `springboot-junit`
- `ktor-kotlin-test`
- `kmp-library`
- `android-app`

There is also a harness at `smoke/run-agent-smoke.sh` that runs Codex and OpenCode against those projects without telling them to use `build-brief` explicitly. The point is to see whether the installed agent instructions are enough on their own.

Useful harness options:

- `--case <case-id>` to run one project/test prompt at a time
- `--timeout <seconds>` to cap each agent invocation so one stuck run does not block the whole matrix

Each harness run now writes to its own timestamp-and-pid output directory under `smoke/out/`, and updates `smoke/out/latest` to point at the newest run.

In local validation on this machine:

- Codex used `build-brief` for JVM, Spring Boot, Ktor, and KMP prompts
- OpenCode instructions alone were not reliable enough on this machine
- after adding the managed OpenCode plugin, the same Spring Boot smoke prompt executed `build-brief clean` and `build-brief test`
- the Android smoke project is in place, but full Android test runs are still the slow lane and should be treated separately from the fast loop

## license

MIT
