# Gradle Output Reduction Tool Plan (`build-brief`)

## Problem

Design a reusable tool that reduces noisy Gradle output for humans, CLIs, and agentic AI tools in the same spirit as RTK: preserve the important signal, avoid polluting the context window, and still leave a path to inspect raw logs when needed.

This planning task is **greenfield**. The current working directory does not contain the target implementation codebase, so this plan assumes a new standalone project/tool rather than changes to an existing Gradle or CLI repository.

Planned project directory: **`/Users/staticvar/Projects/build-brief`**

## Working Project Name

Selected name: **`build-brief`**

Why this name:
- It highlights the main value proposition: turn long build logs into a concise, useful brief.
- It leaves room to support more than Gradle later, even though Gradle is the immediate focus.
- It sounds like a product rather than just a thin wrapper script.

## Current State

- No relevant implementation repository is available in the current workspace.
- The intended project home for implementation is `/Users/staticvar/Projects/build-brief`, outside the current brainstorming directory.
- There is no existing Gradle wrapper/reducer implementation to extend.
- The only directly relevant artifact in this workspace is an example hook config under `.github/hooks/`, which is useful as evidence that hook-based integrations are possible in some CLIs, but it is **not** part of the target project.

## Goals

- Work with both `gradle` and project-local `./gradlew` / `gradlew.bat`.
- Be safe for a wide range of Gradle ecosystems: Spring Boot, Ktor, Android, Kotlin Multiplatform, plain JVM, and multi-project builds.
- Be easy to integrate into multiple CLIs and agentic tools, not tied to one vendor.
- Reduce output by default while preserving failures, warnings, test summaries, and final build status.
- Offer a deterministic interface so AI tools can rely on it.

## Non-Goals

- Modifying every Gradle project with a mandatory plugin.
- Replacing Gradle itself.
- Hiding failures or stack traces when they are the key diagnostic signal.

## Recommended Product Shape

Build this as a **standalone executable with a stable machine-facing contract**, then add integration adapters around it.

Recommended initial shape:

1. **Core binary**: executes Gradle, captures stdout/stderr, classifies lines/events, emits reduced output plus a structured summary.
2. **CLI adapter**: `build-brief` as the primary executable, with room for a shorter alias later if desired.
3. **Agent/tool integrations**:
   - shell alias/function templates
   - Copilot/Claude/etc. instruction snippets
   - optional hook examples for CLIs that support pre/post command hooks
4. **Raw log retention**: write full output to a temp/log file and expose the path when the reduced output omits detail.

## Architecture Options

### Option A — Outer process wrapper (recommended for v1)

The tool launches `gradlew` or `gradle`, forces stable console settings where appropriate, reads stdout/stderr, reduces noise, and returns concise output.

**Pros**
- Works across nearly all Gradle projects with no project changes.
- Easy to integrate into many CLIs and agentic tools.
- No mandatory JVM dependency beyond what Gradle already needs.
- Best portability across macOS/Linux/Windows.

**Cons**
- Only sees text unless deeper Gradle integration is added.
- Must be careful not to over-filter important information.
- Rich progress UIs and interleaved stderr/stdout can be tricky to normalize.

### Option B — Gradle-native init script / injected listener

Use `--init-script` to attach Gradle-side logic that emits structured events or a reduced view.

**Pros**
- Better awareness of tasks, lifecycle phases, and outcomes.
- Can produce more semantically accurate summaries.

**Cons**
- More compatibility testing across Gradle versions.
- Harder distribution story.
- More moving parts for users and agent tools.
- Greater risk of behavioral differences across project types.

### Option C — Tooling API / JVM sidecar

Drive Gradle through the Tooling API and consume structured progress/events.

**Pros**
- Strongest structured model.
- Best long-term path for robust summaries and machine-readable progress.

**Cons**
- Requires JVM-side implementation or sidecar.
- More complex packaging and invocation.
- Harder to adopt as a simple drop-in command.

## Recommended Technical Direction

Use a **two-layer strategy**:

- **V1**: standalone outer wrapper with excellent command resolution, stable console coercion, a carefully designed reduction pipeline, and structured JSON output.
- **V2**: optional deeper Gradle-aware integration via init script or Tooling API when projects or host tools want richer task/event semantics.

This keeps the first release broadly usable while preserving a path to higher fidelity later.

## Where the Output Reduction Logic Should Live

### Core logic location (recommended)

Put the reduction logic in a **shared core library inside the standalone tool**, not in per-CLI hooks or prompt instructions.

Why:
- Hooks and instructions are good entry points, but they are not portable enough to be the source of truth.
- A real executable gives one stable contract to many hosts.
- The core library can later back a CLI, editor extension, or daemon.

### What hooks are good for

Use hooks as **optional integrations**, not the primary implementation:

- pre-run: rewrite `gradle` calls to the wrapper command in tools that support interception
- post-run: attach summaries, archive raw logs, or send telemetry
- session hooks: inject environment variables or helper aliases

**Pros**
- Zero-touch ergonomics in supported CLIs.
- Good for local/team workflow automation.

**Cons**
- Tool-specific and inconsistent across ecosystems.
- Often not authoritative enough to guarantee usage.

### What custom instructions are good for

Use custom instructions as **behavioral guidance** for agents:

- “Prefer `gradle-rtk test` over `./gradlew test`”
- “Use raw Gradle only when the reduced output is insufficient”

**Pros**
- Fast to adopt.
- Works well with agentic tools that already honor repo/user instructions.

**Cons**
- Soft enforcement only.
- Quality depends on model compliance.
- Each tool has different instruction file conventions.

### Better universal entry points

Prioritize these entry points:

1. **Direct executable** (`build-brief ...`)
2. **Shell alias/function wrappers**
3. **Custom instructions/templates**
4. **CLI-specific hooks**

## Core Functional Requirements

- Resolve command target in this order:
  1. explicit user-provided path
  2. project-local wrapper
  3. system `gradle`
- Preserve pass-through arguments.
- Default to stable/log-parseable Gradle settings where safe, especially `--console=plain`.
- Detect and handle interactive or long-running commands carefully.
- Preserve exit codes exactly.
- Keep full raw logs accessible.
- Emit concise summaries for:
  - build success/failure
  - failed tasks
  - failed tests
  - warnings count / noteworthy warnings
  - duration
  - success summary
- Offer modes such as:
  - `human`
  - `agent`
  - `raw`

## Reduction Pipeline

1. **Command resolution**: choose wrapper vs system Gradle.
2. **Execution policy**: inject safe flags/env vars for stable output.
3. **Stream normalization**: remove ANSI noise, normalize line buffering, separate stdout/stderr metadata.
4. **Classification**:
   - task lifecycle
   - compiler/test output
   - warnings/deprecations
   - stack traces
   - download/progress noise
5. **Aggregation**:
   - collapse repetitive task lines
   - group repeated warnings
   - keep first occurrence + counters
6. **Decision layer**:
   - always show failures and final result
   - show warnings based on mode/policy
   - reveal raw log path when details are suppressed
7. **Output contracts**:
   - concise terminal summary
   - structured JSON summary for tools

## Implementation Best Practices

- Preserve Gradle exit codes exactly.
- Prefer **fail-open behavior** when reduction logic is uncertain: show more output, not less.
- Never suppress stack traces, failed tasks, or failing test names when they are the main diagnostic signal.
- Keep the JSON schema versioned and deterministic so agent tooling can depend on it.
- Build the reducer against recorded fixture logs from multiple Gradle ecosystems before optimizing heuristics.
- Make raw-log retention a first-class feature, not a debugging afterthought.
- Keep config precedence simple and explicit: CLI flags, then env vars, then config file, then defaults.
- Treat cross-platform process handling as a requirement from day one: `gradlew`, `gradlew.bat`, quoting, working directory, and signal forwarding.
- Avoid Gradle-project mutation in v1; the wrapper should be safe to use without editing user builds.

## Cross-Project Compatibility Notes

- **Spring Boot / Ktor / JVM**: usually easiest; mostly conventional test/build output.
- **Android**: large task graphs and noisier plugin output; needs stronger collapsing and good failed-task surfacing.
- **KMP**: multiple targets produce repetitive task noise; summarization must group by target/task family.
- **Multi-project builds**: task path handling and per-project aggregation are essential.

## Implementation Language Trade-offs

### Go (selected)

**Pros**
- Single static binary distribution.
- Excellent subprocess and streaming ergonomics.
- Easy cross-platform delivery.
- Good fit for a wrapper-first CLI with structured JSON output.

**Cons**
- Deep Gradle-native integration would still need JVM interop or a secondary component.

### Rust

**Pros**
- Excellent performance and single-binary distribution.
- Strong correctness story.

**Cons**
- Slower iteration for subprocess-heavy text tooling compared with Go for many teams.

### Kotlin/Java (strong candidate if a JVM requirement is acceptable)

**Pros**
- Best path if Tooling API or deep Gradle integration is the center of gravity.
- Native familiarity with Gradle internals.
- Very reasonable for this problem domain because most target users already work in Gradle/JDK environments.

**Cons**
- Unless compiled ahead-of-time, the wrapper itself needs a local Java runtime.
- Bundling a runtime with `jlink`/`jpackage` increases artifact size and packaging complexity.
- Less ideal than Go/Rust if the goal is a tiny no-runtime native binary for every host environment.

### Node/Python

**Pros**
- Fast iteration.
- Easy prototype velocity.

**Cons**
- Runtime dependency burden and weaker “install everywhere” story.

## Validation Plan

- Build a corpus of captured outputs from:
  - plain JVM app
  - Spring Boot app
  - Android project
  - KMP project
  - multi-project build
- Test commands:
  - `build`
  - `test`
  - `assemble`
  - failing test
  - compile error
  - dependency resolution noise
- Verify:
  - exit codes preserved
  - failures never hidden
  - raw logs always retrievable
  - JSON summary stable enough for agents
  - Android projects behave well enough for launch quality

## Current Implementation Status

Completed so far:

- standalone Go CLI scaffold with `cmd/build-brief` and internal packages for app, gradle resolution, runner, reducer, and output rendering
- wrapper resolution for explicit path, local `gradlew` / `gradlew.bat`, and system `gradle`
- default `--console=plain` injection unless a console flag is already supplied
- raw log retention plus concise default output and `raw` replay mode
- schema-versioned JSON output (`v1`)
- file-first log handling with a reusable per-project `latest` log file instead of retaining the full Gradle output in memory
- JSON collection fields now emit stable empty arrays instead of `null`
- initial graceful interrupt handling using process-aware cancellation plus a wait window before forced termination
- ANSI stripping, multi-line failure-context capture, and improved highlight prioritization in the reducer
- strict wrapper-flag parsing that requires `--` for pass-through Gradle flags
- installer flows for `--install`, `--install-force`, and interactive `--global` agent detection/selection, with global installs limited to existing instruction files
- a tool registry for Copilot CLI, Claude Code, Codex CLI, OpenCode, and Gemini CLI with known global instruction-file targets
- real-agent smoke testing against OpenCode and Codex using a local Gradle sample project
- console-first hybrid failure enrichment with fail-open JUnit parsing and explicit compiler/syntax error capture from Gradle output
- README plus example instruction, shell, and hook guidance files
- unit tests for argument parsing, resolver behavior, reducer behavior, and representative fixture logs for Android, Ktor, Spring Boot, and KMP-style output
- runner tests covering reusable log paths, exit-code-preserving log capture, and cancellation behavior
- build/test/smoke-test verification using the local Go toolchain and fake Gradle executables
- end-to-end validation with `/Users/staticvar/Projects/build-brief-smoke`, including successful and failing Gradle tasks routed through `build-brief`
- main-source error validation in `/Users/staticvar/Projects/build-brief-smoke`, including Java syntax and symbol-resolution compile failures

## Real Agent Validation

The installer and instruction flow have now been validated against real local agent CLIs, not just unit tests.

- **OpenCode**
  - Installed the managed `build-brief` block into `~/.config/opencode/AGENTS.md`
  - Verified `opencode run` used `build-brief compileJava`
  - Verified `opencode run` used `build-brief smokeFail`
  - Confirmed raw-log reuse and surfaced failure details from the `build-brief` output

- **Codex CLI**
  - Installed the managed `build-brief` block into `~/.codex/AGENTS.md`
  - Verified `codex exec` used `build-brief compileJava`
  - Verified `codex exec` used `build-brief` successfully for smoke failure validation
  - Confirmed Codex extracted the intentional failure reason from the structured `build-brief` output

- **Important finding from real-world validation**
  - OpenCode on this macOS machine used `~/.config/opencode/AGENTS.md`, not only `os.UserConfigDir()/opencode/AGENTS.md`
  - The tool registry was updated to target the OpenCode roots the CLI actually loads here: `~/.config/opencode/...` and `~/.opencode/...`
  - Global instructions alone were not reliable enough for OpenCode in later smoke validation
  - A managed OpenCode plugin plus a reusable `build-brief rewrite` command became the stronger integration path
  - The smoke harness was also hardened:
    - it still runs one prompt per case, but now supports `--case` for direct single-case runs
    - per-case timeouts prevent one stalled agent startup from blocking the full matrix
    - each harness invocation writes to its own output directory instead of sharing one mutable `out/latest` directory
    - transcript counting now correctly handles ANSI-prefixed OpenCode logs and Codex shell-exec log lines

## Post-Implementation Review Findings

Two review rounds from **Gemini 3 Pro** and **Claude Opus 4.6** now point to a narrower, mostly non-critical follow-up queue.

Implemented in response to that review:

1. **Reducer robustness**
   - multi-line failure context is now captured after headers like `* What went wrong:`
   - ANSI codes are stripped before pattern matching
   - warning lines no longer crowd out more important failure lines in `ImportantLines`
2. **Test coverage**
   - runner tests now cover raw-log reuse, exit-code preservation, and cancellation behavior
3. **CLI strictness**
   - unknown `build-brief` flags now fail fast and instruct callers to use `--` for Gradle pass-through flags

Notable reviewer disagreement:

- Gemini described current signal handling as acceptable at a high level, while Opus flagged the shutdown path as a real issue. The implementation now includes process-aware interrupt handling plus signal-exit normalization, but real-world validation against representative Gradle builds is still valuable.

Second end-to-end review summary:

- **Both models agree** the implementation matches the intended wrapper-first product shape well.
- **Both models agree** the code structure is strong and the core runner/reducer/output split is appropriate.
- **Both models agree** the biggest remaining gap is not architecture but **reducer realism**: larger, noisier real-world Gradle fixtures are still needed.
- **Gemini** now sees the tool as effectively production-ready, with mostly polish items left.
- **Opus** remains more conservative and highlights a few medium-priority follow-ups:
  - Windows cancellation is still weak compared with Unix
  - reducer fixture size is still too small for high confidence
  - output rendering lacks direct tests
  - a few UX niceties like `--version` are still missing

## Open Questions

- Whether the tool should merely wrap execution or also provide installable shell integrations/templates.

## Proposed Execution Phases

1. **Requirements and contracts**
   - finalize command surface, output modes, and support matrix
   - keep the JSON schema contract stable for machine consumers
2. **Core wrapper**
    - command resolution, execution, exit-code preservation, raw log capture
    - keep the file-first log pipeline and graceful cancellation behavior robust
    - maintain installer and agent-detection behavior as tool ecosystems evolve
3. **Reducer engine**
   - normalization, classification, aggregation, summarization
   - continue expanding heuristics using larger real-world Gradle logs
4. **Machine interfaces**
   - JSON output and stable summary schema
   - preserve deterministic empty-array semantics for collection fields
5. **Integrations**
   - shell templates, hook examples, instruction templates
6. **Compatibility matrix**
   - validate against representative Gradle project types
   - expand realistic fixtures and runner-level tests

## Recommended Next Steps

1. Validate the current behavior against larger real-world Gradle logs and projects
2. Expand fixtures for download noise, stack traces, and multi-project output
3. Add direct tests for the `output` package
4. Decide whether installable shell integrations and tool-specific hook configs should move from guidance into first-class automation

## Initial Recommendation Summary

- Build a **standalone wrapper-first tool**.
- Use **`build-brief`** as the project and primary executable name.
- Keep the **reduction logic in the tool core**, not in hooks or prompt files.
- Use **hooks and custom instructions as optional adoption helpers**.
- Make **JSON output part of v1**, but avoid MCP/tool-server scope unless the project grows.
- Treat **Android as a launch requirement** in the validation matrix.
- Because the wrapper itself must be a **native binary with no Java requirement**, do **not** choose Kotlin/JVM for v1.
- Use **Go** for v1; keep **Rust** as the fallback if binary minimalism and lower-level control become more important than implementation speed.
