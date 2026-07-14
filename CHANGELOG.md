# Changelog

## v0.2.0 - 2026-07-14

- chore(smoke): ignore nested Android build output (d54b0d9)
- fix(smoke): remove obsolete manifest package (27257ac)
- fix(smoke): migrate Android fixture to built-in Kotlin (779e24b)
- test: stabilize stale junit candidate fixture (d7521d9)
- fix: align junit selection with report targets (aea15dc)
- fix: compose junit scan incompleteness reasons (ca6d1c3)
- fix: preserve walk truncation compatibility (e4582da)
- fix: preserve incomplete junit fresh scans (bd78ed8)
- fix: select fresh junit reports before capping (aac0b8a)
- fix: stabilize build scan completeness metadata (07ecfb2)
- fix: suppress stale junit scan metadata (02b0ab1)
- fix: fail closed on invalid tracking lock metadata (d8997f4)
- fix: reclaim tracking locks after PID reuse (3a7a263)
- fix: use unique tracking temp files (aa7a084)
- test: normalize Pages CRLF fixtures (ff32f0f)
- test: support CRLF Pages workflow fixtures (52cc389)
- fix(release): resolve workflow YAML aliases (7d9520b)
- test(release): parse run scalars with YAML AST (5a23656)
- fix: avoid tracking permission repair on symlinks (920a7a3)
- test(release): reject all shell expressions (d15d9ae)
- fix(release): prevent shell expression injection (97adcd6)
- fix: restrict tracking file permissions (718db1f)
- Pin GitHub Pages actions by SHA (67eb64b)
- fix: harden release docs and diagnostics (6adc1d2)
- fix: make Kotlin daemon recovery actionable (10a39ff)
- docs: validate website Gradle flag example (b37fe64)
- test: clarify release dry-run safety (e3f33ae)
- docs: correct daemon guidance (51bc799)
- ci: make release dispatch dry-run safe (af4698b)
- docs: clean up instruction drift (14cc962)
- fix: align GitHub workflow command escaping (714b1cf)
- fix: neutralize legacy GitHub workflow commands (861653f)
- fix: neutralize trimmed GitHub workflow commands (9ed764a)
- fix: harden GitHub CI summaries (1d49dc5)
- docs: clarify gains retention behavior (8451398)
- feat: add explicit CI mode (82ba0b3)
- feat: present local gains period (7d94434)
- feat: warn on custom match rule cap (c1e5fa8)
- test: add reducer quality corpus fixtures (1f27afe)
- fix: lazily allocate snapshot heap storage (7bf5ee1)
- perf: skip informational enrichment and optimize snapshots (efabc75)
- fix: preserve full reducer invocation semantics (6aca751)
- fix: complete CI metadata handling (fb5edca)
- test: make reclaimed lock release portable (6382008)
- fix: preserve successor tracking locks on release (db6c32d)
- fix: serialize stale tracking lock reclamation (5336578)
- fix: retry contended tracking lock removal on Windows (d792f52)
- fix: keep Windows cancellation helper alive (9ceaec9)
- fix: make runner tests Windows-compatible (5092bf4)
- fix: stabilize Windows CI fixtures (794ef25)
- fix: isolate tracking tests on Windows (16b00d9)
- fix: report artifact scan completeness (b3392ab)
- fix: bound enrichment capture metadata (3e58f8d)
- fix: bound reducer completeness and input truncation (78d3b1b)
- test: preserve nested quoted framework hints (98ff49c)
- fix: finalize artifact hint and reducer bounds (54d3e5f)
- test: reject unterminated smoke TSV records (af44852)
- fix: harden artifact hint coverage and streaming (634402c)
- test: validate literal smoke TSV grammar (aad1b71)
- fix: bound artifact hint retention (3be3a2a)
- test: validate smoke fixture schema (3e74454)
- docs: clarify relative config paths on site (db160c4)
- fix: harden enrichment scan bounds and scoping (8e0368f)
- test: cover relative config directory precedence (f40495b)
- ci: check full history on new ref pushes (71512b2)
- fix: preserve enrichment scan completeness (66449a5)
- ci: harden workflow checks (c7edf99)
- docs: clarify relative config paths (4a4996a)
- feat: report enrichment scan completeness (30b1d9f)
- ci: add cross-platform quality checks (7453d4b)
- fix: resolve relative config paths from project dir (024aec7)
- test: make raw log finalization failure deterministic (a52cbe3)
- fix runner capture truncation after failed builds (7b048ac)
- fix: fail on wrapper output errors (2b5a55c)
- fix: label ancillary pruning warnings (d5b676d)
- fix runner output capture lifecycle (3adaccd)
- pi-agent: Fix output stream ordering (a5e4ef4)
- fix: continue after token estimation errors (3a4bdca)
- fix: preserve safe legacy tracking labels (a9faa62)
- fix: migrate legacy tracking labels safely (3f04064)
- fix: continue summary after ancillary runner errors (a1d5239)
- fix: address redaction review findings (dfaa642)
- test: strengthen exit preservation coverage (aae2a60)
- pi-agent: Preserve Gradle exit code (3769a5d)
- pi-agent: Implement command redaction (0ca671e)

## v0.1.0 - 2026-06-20

- fix reducer Kotlin diagnostics and scan links (#21) (107ab4f)
- fix: align doctor json mode compatibility (#20) (f7af0b1)
- feat: add gradle failure diagnostics (#19) (9c1c4f1)
- feat: add build brief doctor (#18) (c8f69d1)

## v0.0.12 - 2026-06-01

- Add generated changelog to release notes (#17) (8eb872e)
- Fix installer release downloads (#16) (e3c9ed3)
- Fix configuration cache report duplication (#15) (2320a7c)
- Surface Gradle Configuration Cache status and problems (#11) (764017d)
- Preserve Gradle report output (#12) (358c1ac)

## v0.0.11 - 2026-05-20

- Fix Claude hook stdin payload (996b73f)

## v0.0.10 - 2026-05-16

- Support custom regex matches (5577a7d)

## v0.0.9 - 2026-05-10

- Surface Develocity build scan URLs (c85b266)

## v0.0.8 - 2026-05-10

- Support Pi extension across package scopes (288de15)
- Update OpenCode website integration copy (3017f92)
- Fix agent card grid layout (4dfc91f)

## v0.0.7 - 2026-05-10

- Fix Codex app directory detection (d235f13)
- Refine Codex plugin installation (22de30d)
- Add Pi integration and Codex marketplace bundle (5c724b9)
- Update Codex hooks feature flag (ec6d93c)

## v0.0.6 - 2026-04-03

- Add interactive global installer (b660a20)
- Add chained Gradle rewrite examples and tests (882aa82)
- Add managed plugin installs for supported CLIs (9f3cae8)
- Update GitHub funding configuration (5e59189)
- docs: add social card asset (b955713)
- docs: add footer credit (1957c36)
- docs: improve site metadata (13063f4)
- docs: refresh gains examples (d5457f2)

## v0.0.5 - 2026-03-21

- fix: clean up gains tracking labels (9a4f287)
- docs: fix website examples (adef13a)
- docs: show test count examples (8ef20e5)
- feat: report test pass and fail counts (9b01755)
- docs: redesign website (731dd76)
- docs: tighten site layout (8aed3d3)
- docs: polish gains section (4336568)
- docs: add gains usage section (627e048)

## v0.0.4 - 2026-03-16

- fix: harden homebrew formula install (4f812ad)
- fix: harden tracking rewrite and raw logs (932535d)
- refactor: merge RTK guidance into managed installs (dcee73e)
- docs: trim README and normalize titles (959b27a)
- docs: refresh agent integration and site copy (550c6a1)
- feat: add Claude Code hook example (0c9bb85)
- docs: use real output examples (e329675)
- refactor: preserve Gradle daemon reuse (f3e2b99)

## v0.0.3 - 2026-03-15

- fix: surface warm artifact outputs (8d0b6b6)
- feat: harden installer asset resolution (3a0f907)

## v0.0.2 - 2026-03-15

- feat: publish hosted installer entrypoint (f49d07b)
- docs: align README and site with current behavior (16421e4)
- chore: harden release automation (5cea2ce)
- refactor: keep gains focused on token savings (b690e36)
- feat: report generated gradle artifacts (4c79d3a)
- feat: simplify gradle invocation handling (ea8b4bc)
- Redesign landing page (0de32fb)
- Enable Pages bootstrap in workflow (d0681d6)
- Add build-brief landing page (f5b9f1f)
- Update README Homebrew install commands (0e719ca)

## v0.0.1 - 2026-03-15

- Track reducer fixture logs (161db30)
- Add Homebrew tap release automation (d3f07c5)
- Harden log processing and tracking safety (6952c91)
- Improve runtime progress and raw output handling (d76350e)
- Refine installer RTK detection behavior (0f19ebb)
- Add build-brief integrations and smoke validation (372af52)
- Improve Windows cancellation handling (829865d)
- Add version flag (f085a61)
- Add integration examples (c713175)
- Add runner and process handling (97d63bc)
- Add log reduction and output rendering (7e43611)
- Add CLI entrypoint and Gradle resolution (cb78bf4)
- Initialize build-brief project docs (804fc9d)

Releases prepend new entries here.
