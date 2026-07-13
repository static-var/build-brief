package smoke

import (
	"bufio"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCasesFixtureSchema(t *testing.T) {
	file, err := os.Open("cases.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	raw := bufio.NewScanner(file)
	line := 0
	for raw.Scan() {
		line++
		if strings.HasSuffix(raw.Text(), "\t") {
			t.Errorf("line %d: empty terminal skip_when must be omitted, not represented by a trailing tab", line)
		}
	}
	if err := raw.Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}

	reader := csv.NewReader(file)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	wantHeader := []string{"case_id", "project_rel", "prompt_file", "expect_snippet", "skip_when"}
	if strings.Join(header, "\x00") != strings.Join(wantHeader, "\x00") {
		t.Fatalf("unexpected header: %q", header)
	}

	for line := 2; ; line++ {
		fields, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read line %d: %v", line, err)
		}
		if len(fields) != 4 && len(fields) != 5 {
			t.Fatalf("line %d: expected four fields plus optional skip_when, got %d", line, len(fields))
		}
		if len(fields) == 4 {
			fields = append(fields, "")
		}
		for index, field := range fields {
			if strings.TrimSpace(field) != field {
				t.Errorf("line %d field %d: unexpected surrounding whitespace", line, index+1)
			}
		}
		if fields[0] == "" || fields[1] == "" || fields[2] == "" || fields[3] == "" {
			t.Errorf("line %d: required field is empty", line)
		}
		if _, err := os.Stat(filepath.Clean(fields[1])); err != nil {
			t.Errorf("line %d project %q: %v", line, fields[1], err)
		}
		if _, err := os.Stat(filepath.Clean(fields[2])); err != nil {
			t.Errorf("line %d prompt %q: %v", line, fields[2], err)
		}
		if fields[4] != "" && fields[4] != "missing-android-sdk" {
			t.Errorf("line %d: unknown skip_when value %q", line, fields[4])
		}
	}
}
