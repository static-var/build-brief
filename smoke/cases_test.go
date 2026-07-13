package smoke

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const casesHeader = "case_id\tproject_rel\tprompt_file\texpect_snippet\tskip_when"

func TestCasesFixtureSchema(t *testing.T) {
	file, err := os.Open("cases.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if err := validateCasesFixture(file); err != nil {
		t.Fatal(err)
	}
}

func TestCasesFixtureSchemaRejectsQuotedTab(t *testing.T) {
	content := casesFixtureContents(t)
	content = strings.Replace(content, "BUILD SUCCESSFUL", "\"BUILD\tSUCCESSFUL\"", 1)

	assertCasesFixtureRejected(t, content)
}

func TestCasesFixtureSchemaRejectsCRLF(t *testing.T) {
	content := strings.ReplaceAll(casesFixtureContents(t), "\n", "\r\n")

	assertCasesFixtureRejected(t, content)
}

func TestCasesFixtureSchemaRejectsCR(t *testing.T) {
	content := strings.Replace(casesFixtureContents(t), "\n", "\r", 1)

	assertCasesFixtureRejected(t, content)
}

func TestCasesFixtureSchemaRejectsMissingFinalNewline(t *testing.T) {
	content := strings.TrimSuffix(casesFixtureContents(t), "\n")

	assertCasesFixtureRejected(t, content)
}

func TestCasesFixtureSchemaRejectsMultilineField(t *testing.T) {
	content := strings.Replace(casesFixtureContents(t), "cannot find symbol", "\"cannot\nfind symbol\"", 1)

	assertCasesFixtureRejected(t, content)
}

func validateCasesFixture(r io.Reader) error {
	reader := bufio.NewReader(r)
	line := 0
	for {
		raw, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		if err == io.EOF {
			if len(raw) > 0 {
				return fmt.Errorf("line %d: record must end with newline", line+1)
			}
			break
		}
		line++
		raw = strings.TrimSuffix(raw, "\n")
		if strings.ContainsAny(raw, "\r\x00") {
			return fmt.Errorf("line %d: carriage return or NUL is not allowed", line)
		}
		if strings.ContainsAny(raw, "'\"") {
			return fmt.Errorf("line %d: quotes are not allowed in literal TSV", line)
		}

		fields := strings.Split(raw, "\t")
		if line == 1 {
			if raw != casesHeader {
				return fmt.Errorf("unexpected header: %q", fields)
			}
			if err == io.EOF {
				break
			}
			continue
		}

		if len(fields) != 4 && len(fields) != 5 {
			return fmt.Errorf("line %d: expected four fields plus optional skip_when, got %d", line, len(fields))
		}
		if len(fields) == 5 && fields[4] == "" {
			return fmt.Errorf("line %d: empty terminal skip_when must be omitted, not represented by a trailing tab", line)
		}
		if len(fields) == 4 {
			fields = append(fields, "")
		}
		for index, field := range fields {
			if strings.TrimSpace(field) != field {
				return fmt.Errorf("line %d field %d: unexpected surrounding whitespace", line, index+1)
			}
		}
		if fields[0] == "" || fields[1] == "" || fields[2] == "" || fields[3] == "" {
			return fmt.Errorf("line %d: required field is empty", line)
		}
		if _, err := os.Stat(filepath.Clean(fields[1])); err != nil {
			return fmt.Errorf("line %d project %q: %w", line, fields[1], err)
		}
		if _, err := os.Stat(filepath.Clean(fields[2])); err != nil {
			return fmt.Errorf("line %d prompt %q: %w", line, fields[2], err)
		}
		if fields[4] != "" && fields[4] != "missing-android-sdk" {
			return fmt.Errorf("line %d: unknown skip_when value %q", line, fields[4])
		}
		if err == io.EOF {
			break
		}
	}
	if line == 0 {
		return fmt.Errorf("missing header")
	}
	return nil
}

func casesFixtureContents(t *testing.T) string {
	t.Helper()

	content, err := os.ReadFile("cases.tsv")
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func assertCasesFixtureRejected(t *testing.T, content string) {
	t.Helper()

	if err := validateCasesFixture(strings.NewReader(content)); err == nil {
		t.Fatal("expected malformed fixture to be rejected")
	}
}
