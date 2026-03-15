package rewrite

import (
	"regexp"
	"strings"
)

var (
	envPrefixPattern     = regexp.MustCompile(`^((?:[A-Za-z_][A-Za-z0-9_]*=[^\s]+\s+)*)`)
	gradleCheckPattern   = regexp.MustCompile(`^(?:which|command\s+-v)\s+(?:gradle|\.{0,2}/gradlew(?:\.bat)?|gradlew(?:\.bat)?)$`)
	gradleCommandPattern = regexp.MustCompile(`^(?:gradle|\.{0,2}/gradlew(?:\.bat)?|gradlew(?:\.bat)?)(?:\s+(.*))?$`)
)

type part struct {
	text        string
	isSeparator bool
}

func ShellCommand(command string) (string, bool) {
	if strings.TrimSpace(command) == "" {
		return command, false
	}

	parts := splitCommandChain(command)
	changed := false
	for i := range parts {
		if parts[i].isSeparator {
			continue
		}

		rewritten, partChanged := rewriteSegment(parts[i].text)
		if partChanged {
			parts[i].text = rewritten
			changed = true
		}
	}

	if !changed {
		return command, false
	}

	return joinCommandChain(parts), true
}

func rewriteSegment(segment string) (string, bool) {
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" || strings.Contains(trimmed, "build-brief") || strings.Contains(trimmed, "|") {
		return trimmed, false
	}

	envPrefix := envPrefixPattern.FindString(trimmed)
	remainder := strings.TrimSpace(strings.TrimPrefix(trimmed, envPrefix))
	envPrefix = strings.TrimSpace(envPrefix)

	if gradleCheckPattern.MatchString(remainder) {
		return combineSegment(envPrefix, "command -v build-brief"), true
	}

	matches := gradleCommandPattern.FindStringSubmatch(remainder)
	if matches == nil {
		return trimmed, false
	}

	rewritten := "build-brief"
	gradleExecutable := strings.Fields(remainder)[0]
	rewritten += " " + gradleExecutable
	if rest := strings.TrimSpace(matches[1]); rest != "" {
		rewritten += " " + rest
	}

	return combineSegment(envPrefix, rewritten), true
}

func combineSegment(prefix, command string) string {
	if prefix == "" {
		return command
	}
	return prefix + " " + command
}

func joinCommandChain(parts []part) string {
	var builder strings.Builder
	for _, part := range parts {
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(strings.TrimSpace(part.text))
	}
	return builder.String()
}

func splitCommandChain(command string) []part {
	var parts []part
	var builder strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	flushSegment := func() {
		text := strings.TrimSpace(builder.String())
		builder.Reset()
		if text != "" {
			parts = append(parts, part{text: text})
		}
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]

		if escaped {
			builder.WriteByte(ch)
			escaped = false
			continue
		}

		switch ch {
		case '\\':
			escaped = true
			builder.WriteByte(ch)
			continue
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		}

		if !inSingleQuote && !inDoubleQuote {
			if ch == ';' {
				flushSegment()
				parts = append(parts, part{text: ";", isSeparator: true})
				continue
			}
			if i+1 < len(command) {
				next := command[i+1]
				if ch == '&' && next == '&' {
					flushSegment()
					parts = append(parts, part{text: "&&", isSeparator: true})
					i++
					continue
				}
				if ch == '|' && next == '|' {
					flushSegment()
					parts = append(parts, part{text: "||", isSeparator: true})
					i++
					continue
				}
			}
		}

		builder.WriteByte(ch)
	}

	flushSegment()
	return parts
}
