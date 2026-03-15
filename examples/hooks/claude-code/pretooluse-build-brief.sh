#!/usr/bin/env bash

set -euo pipefail

if [[ "${CLAUDE_TOOL_NAME:-}" != "Bash" ]]; then
  exit 0
fi

if ! command -v build-brief >/dev/null 2>&1; then
  exit 0
fi

tool_input="${CLAUDE_TOOL_INPUT:-}"
if [[ -z "$tool_input" ]]; then
  exit 0
fi

original_command="$(python3 - "$tool_input" <<'PY'
import json
import sys

try:
    payload = json.loads(sys.argv[1])
except Exception:
    print("")
    raise SystemExit(0)

command = payload.get("command", "")
print(command if isinstance(command, str) else "")
PY
)"

if [[ -z "$original_command" ]]; then
  exit 0
fi

rewritten_command="$(build-brief rewrite "$original_command" 2>/dev/null || true)"
if [[ -z "$rewritten_command" || "$rewritten_command" == "$original_command" ]]; then
  exit 0
fi

cat >&2 <<EOF
[build-brief] Claude Code intercepted a routine Gradle command.
[build-brief] Use this instead:

  $rewritten_command

This Claude Code PreToolUse hook can block and suggest a safer replacement,
but it cannot transparently rewrite and continue the way the managed OpenCode
plugin does.
EOF

exit 2
