#!/usr/bin/env bash

set -euo pipefail

if ! command -v build-brief >/dev/null 2>&1; then
  exit 0
fi

payload="$(cat)"
if [[ -z "$payload" ]]; then
  exit 0
fi

original_command="$(python3 - "$payload" <<'PY'
import json
import sys

try:
    payload = json.loads(sys.argv[1])
except Exception:
    print("")
    raise SystemExit(0)

if not isinstance(payload, dict):
    print("")
    raise SystemExit(0)

tool_name = payload.get("tool_name", "")
if tool_name != "Bash":
    print("")
    raise SystemExit(0)

tool_input = payload.get("tool_input", {})
if not isinstance(tool_input, dict):
    print("")
    raise SystemExit(0)

command = tool_input.get("command", "")
print(command if isinstance(command, str) else "")
PY
)"

if [[ -z "$original_command" || "$original_command" == *"build-brief"* ]]; then
  exit 0
fi

rewritten_command="$(build-brief rewrite "$original_command" 2>/dev/null || true)"
if [[ -z "$rewritten_command" || "$rewritten_command" == "$original_command" ]]; then
  exit 0
fi

cat >&2 <<EOF
[build-brief] Claude Code intercepted a routine Gradle command or chain.
[build-brief] Use this instead:

  $rewritten_command

This Claude Code PreToolUse hook can block and suggest a safer replacement,
but it cannot transparently rewrite and continue the way the managed OpenCode
plugin does.
EOF

exit 2
