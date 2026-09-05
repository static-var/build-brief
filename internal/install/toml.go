package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type tomlEdit struct {
	section, key, value string
	remove              bool
}

func editTOMLFile(path string, edits ...tomlEdit) error {
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	updated := content
	for _, edit := range edits {
		updated, err = editTOML(updated, edit.section, edit.key, edit.value, edit.remove)
		if err != nil {
			return fmt.Errorf("update %s: %w", path, err)
		}
	}
	if bytes.Equal(content, updated) {
		return nil
	}
	return replaceConfigFile(path, updated)
}

func validateTOML(content []byte) error {
	var document map[string]any
	return toml.Unmarshal(content, &document)
}

func tomlKey(node *unstable.Node) []string {
	var key []string
	iter := node.Key()
	for iter.Next() {
		key = append(key, string(iter.Node().Data))
	}
	return key
}

func editTOML(content []byte, section, key, value string, remove bool) ([]byte, error) {
	if err := validateTOML(content); err != nil {
		return nil, err
	}
	var target []string
	if section != "" {
		var header unstable.Parser
		header.Reset([]byte(section))
		if !header.NextExpression() || header.Expression().Kind != unstable.Table {
			return nil, fmt.Errorf("invalid TOML section %q", section)
		}
		target = tomlKey(header.Expression())
	}
	fullKey := append(slices.Clone(target), key)
	var parser unstable.Parser
	parser.Reset(content)
	var scope []string
	insertion := len(content)
	foundSection := section == ""
	insertionScope := target
	entryKey := key
	inArrayTable := false
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			iter := node.Key()
			iter.Next()
			headerStart := lineStart(content, int(iter.Node().Raw.Offset))
			if foundSection && slices.Equal(scope, insertionScope) {
				insertion = headerStart
			}
			scope = tomlKey(node)
			inArrayTable = node.Kind == unstable.ArrayTable
			if slices.Equal(scope, target) && !inArrayTable {
				foundSection = true
				insertion = len(content)
			}
		case unstable.KeyValue:
			nodeKey := tomlKey(node)
			absoluteKey := append(slices.Clone(scope), nodeKey...)
			if !foundSection && !inArrayTable && len(absoluteKey) > len(target) && len(scope) < len(target) && slices.Equal(absoluteKey[:len(target)], target) {
				foundSection = true
				insertionScope = slices.Clone(scope)
				var parts []string
				for _, part := range fullKey[len(scope):] {
					parts = append(parts, strconv.Quote(part))
				}
				entryKey = strings.Join(parts, ".")
			}
			if inArrayTable || !slices.Equal(absoluteKey, fullKey) {
				continue
			}
			val := node.Value()
			raw := val.Raw
			if val.Kind == unstable.Bool {
				raw = parser.Range(val.Data)
			}
			if raw.Length == 0 || val.Kind == unstable.InlineTable || val.Kind == unstable.Array {
				return nil, fmt.Errorf("cannot edit non-scalar TOML key %s", strings.Join(fullKey, "."))
			}
			start, end := int(raw.Offset), int(raw.Offset+raw.Length)
			if remove {
				iter := node.Key()
				iter.Next()
				start = lineStart(content, int(iter.Node().Raw.Offset))
				end = lineEnd(content, end)
			}
			replacement := value
			if remove {
				replacement = ""
			}
			updated := append([]byte{}, content[:start]...)
			updated = append(updated, replacement...)
			updated = append(updated, content[end:]...)
			if err := validateTOML(updated); err != nil {
				return nil, err
			}
			return updated, nil
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	if remove {
		return content, nil
	}
	entry := entryKey + " = " + value + "\n"
	if !foundSection {
		insertion = len(content)
		entry = section + "\n" + entry
	}
	if insertion > 0 && content[insertion-1] != '\n' {
		entry = "\n" + entry
	}
	updated := append([]byte{}, content[:insertion]...)
	updated = append(updated, entry...)
	updated = append(updated, content[insertion:]...)
	if err := validateTOML(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func lineStart(content []byte, offset int) int {
	return bytes.LastIndexByte(content[:offset], '\n') + 1
}

func lineEnd(content []byte, offset int) int {
	if end := bytes.IndexByte(content[offset:], '\n'); end >= 0 {
		return offset + end + 1
	}
	return len(content)
}

func replaceConfigFile(path string, content []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	} else if !os.IsNotExist(err) {
		return err
	} else if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return err
	}
	mode := os.FileMode(0600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".build-brief-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
