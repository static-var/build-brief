package tracking

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEstimateReaderTokensMatchesString(t *testing.T) {
	text := strings.Repeat("gradle output line with unicode π\n", 2048)

	got, err := EstimateReaderTokens(strings.NewReader(text))
	if err != nil {
		t.Fatalf("estimate reader tokens: %v", err)
	}

	want := EstimateTokens(text)
	if got != want {
		t.Fatalf("expected %d tokens, got %d", want, got)
	}
}

func TestEstimateFileTokensMatchesString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gradle.log")
	text := strings.Repeat("BUILD SUCCESSFUL\n", 4096)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	got, err := EstimateFileTokens(path)
	if err != nil {
		t.Fatalf("estimate file tokens: %v", err)
	}

	want := EstimateTokens(text)
	if got != want {
		t.Fatalf("expected %d tokens, got %d", want, got)
	}
}

func TestRecordRunDropsExpiredRecords(t *testing.T) {
	setTrackingEnv(t)

	path, err := dbPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}

	expired := Record{
		Timestamp:     time.Now().AddDate(0, 0, -retentionDays-1),
		ProjectPath:   "/tmp/project",
		Command:       "gradlew oldTask",
		RawTokens:     100,
		EmittedTokens: 25,
		SavedTokens:   75,
		SavingsPct:    75,
	}
	if err := writeRecords(path, []Record{expired}); err != nil {
		t.Fatalf("seed records: %v", err)
	}

	current := Record{
		Timestamp:     time.Now(),
		ProjectPath:   "/tmp/project",
		Command:       "gradlew test",
		RawTokens:     200,
		EmittedTokens: 50,
		SavedTokens:   150,
		SavingsPct:    75,
		ExecTimeMs:    1234,
	}
	if err := RecordRun(current); err != nil {
		t.Fatalf("record run: %v", err)
	}

	report, err := LoadReport("", true)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if report.Summary.TotalCommands != 1 {
		t.Fatalf("expected 1 retained command, got %d", report.Summary.TotalCommands)
	}
	if len(report.Recent) != 1 || report.Recent[0].Command != current.Command {
		t.Fatalf("unexpected recent records: %+v", report.Recent)
	}
}

func TestRecordRunConcurrentPreservesAllRecords(t *testing.T) {
	setTrackingEnv(t)

	const runs = 12
	start := make(chan struct{})
	errCh := make(chan error, runs)
	var wg sync.WaitGroup

	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errCh <- RecordRun(Record{
				Timestamp:     time.Now().Add(time.Duration(i) * time.Millisecond),
				ProjectPath:   "/tmp/project",
				Command:       fmt.Sprintf("gradlew task-%d", i),
				RawTokens:     100 + i,
				EmittedTokens: 25,
				SavedTokens:   75 + i,
				SavingsPct:    75,
				ExecTimeMs:    int64(1000 + i),
			})
		}(i)
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent record run failed: %v", err)
		}
	}

	report, err := LoadReport("", true)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if report.Summary.TotalCommands != runs {
		t.Fatalf("expected %d commands, got %d", runs, report.Summary.TotalCommands)
	}
}

func TestResetRemovesTrackingData(t *testing.T) {
	setTrackingEnv(t)

	if err := RecordRun(Record{
		Timestamp:     time.Now(),
		ProjectPath:   "/tmp/project",
		Command:       "gradlew test",
		RawTokens:     120,
		EmittedTokens: 30,
		SavedTokens:   90,
		SavingsPct:    75,
	}); err != nil {
		t.Fatalf("record run: %v", err)
	}

	if err := Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	report, err := LoadReport("", true)
	if err != nil {
		t.Fatalf("load report after reset: %v", err)
	}
	if report.Summary.TotalCommands != 0 {
		t.Fatalf("expected empty report after reset, got %+v", report.Summary)
	}
}

func TestAcquireLockFileBreaksStaleLock(t *testing.T) {
	setTrackingEnv(t)

	lockPath := filepath.Join(t.TempDir(), "tracking.jsonl.lock")
	stale := fmt.Sprintf("pid=%d\ncreated_at=%s\n", 999999, time.Now().Add(-staleLockAge-time.Second).UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(lockPath, []byte(stale), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	lockFile, err := acquireLockFile(lockPath, time.Second)
	if err != nil {
		t.Fatalf("acquire lock after stale lock: %v", err)
	}
	releaseLockFile(lockPath, lockFile)
}

func TestRenderTextIncludesRecentHistory(t *testing.T) {
	report := Report{
		Summary: Summary{
			TotalCommands:  2,
			TotalRawTokens: 1000,
			TotalEmitted:   250,
			TotalSaved:     750,
			AvgSavingsPct:  75,
			ByCommand: []CommandAggregate{
				{
					Command:       "gradlew test",
					Count:         2,
					SavedTokens:   750,
					AvgSavingsPct: 75,
				},
			},
		},
		Recent: []Record{
			{
				Timestamp:   time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
				Command:     "gradlew test",
				SavedTokens: 500,
				SavingsPct:  80,
			},
		},
	}

	var out bytes.Buffer
	if err := RenderText(&out, report, true); err != nil {
		t.Fatalf("render text: %v", err)
	}

	text := out.String()
	for _, expected := range []string{
		"build-brief Token Savings (Global Scope)",
		"By Command",
		"Recent Commands",
		"gradlew test",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected rendered report to contain %q, got %q", expected, text)
		}
	}
	for _, unexpected := range []string{
		"Total exec time",
		"Time",
	} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("expected rendered report not to contain %q, got %q", unexpected, text)
		}
	}
}

func TestRenderJSONOmitsTimeFields(t *testing.T) {
	report := Report{
		Summary: Summary{
			TotalCommands:  1,
			TotalRawTokens: 400,
			TotalEmitted:   100,
			TotalSaved:     300,
			AvgSavingsPct:  75,
			ByCommand: []CommandAggregate{
				{
					Command:       "gradlew test",
					Count:         1,
					SavedTokens:   300,
					AvgSavingsPct: 75,
				},
			},
		},
		Recent: []Record{
			{
				Timestamp:     time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
				Command:       "gradlew test",
				SavedTokens:   300,
				SavingsPct:    75,
				ExecTimeMs:    1234,
				RawTokens:     400,
				EmittedTokens: 100,
			},
		},
	}

	var out bytes.Buffer
	if err := RenderJSON(&out, report); err != nil {
		t.Fatalf("render json: %v", err)
	}

	text := out.String()
	for _, unexpected := range []string{
		"exec_time_ms",
		"total_time_ms",
		"avg_time_ms",
	} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("expected rendered json not to contain %q, got %q", unexpected, text)
		}
	}

	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode json: %v", err)
	}
}

func TestSummarizeUsesWeightedOverallSavingsPct(t *testing.T) {
	records := []Record{
		{
			Command:       "gradlew big",
			RawTokens:     1000,
			EmittedTokens: 900,
			SavedTokens:   100,
			SavingsPct:    10,
		},
		{
			Command:       "gradlew tiny",
			RawTokens:     10,
			EmittedTokens: 0,
			SavedTokens:   10,
			SavingsPct:    100,
		},
	}

	summary := summarize(records)

	if summary.TotalRawTokens != 1010 || summary.TotalEmitted != 900 || summary.TotalSaved != 110 {
		t.Fatalf("unexpected aggregate totals: %+v", summary)
	}

	want := SavingsPct(summary.TotalRawTokens, summary.TotalEmitted)
	if summary.AvgSavingsPct != want {
		t.Fatalf("expected weighted overall savings pct %.6f, got %.6f", want, summary.AvgSavingsPct)
	}
	if summary.AvgSavingsPct == 55 {
		t.Fatalf("expected weighted overall savings pct, got naive average %.2f", summary.AvgSavingsPct)
	}
}

func TestLoadReportExcludesRawModeRunsFromGains(t *testing.T) {
	setTrackingEnv(t)

	if err := RecordRun(Record{
		Timestamp:     time.Now().Add(-time.Minute),
		ProjectPath:   "/tmp/project",
		Command:       "gradlew assembleDebug",
		Mode:          "raw",
		RawTokens:     1000,
		EmittedTokens: 1000,
		SavedTokens:   0,
		SavingsPct:    0,
	}); err != nil {
		t.Fatalf("record raw run: %v", err)
	}

	if err := RecordRun(Record{
		Timestamp:     time.Now(),
		ProjectPath:   "/tmp/project",
		Command:       "gradlew assembleDebug",
		Mode:          "human",
		RawTokens:     1000,
		EmittedTokens: 100,
		SavedTokens:   900,
		SavingsPct:    90,
	}); err != nil {
		t.Fatalf("record human run: %v", err)
	}

	report, err := LoadReport("", true)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}

	if report.Summary.TotalCommands != 1 {
		t.Fatalf("expected only human runs in gains report, got %d commands", report.Summary.TotalCommands)
	}
	if report.Summary.TotalSaved != 900 || report.Summary.TotalRawTokens != 1000 || report.Summary.TotalEmitted != 100 {
		t.Fatalf("unexpected filtered totals: %+v", report.Summary)
	}
	if len(report.Recent) != 1 || report.Recent[0].Mode != "human" {
		t.Fatalf("expected recent history to exclude raw runs, got %+v", report.Recent)
	}
	if len(report.Summary.ByCommand) != 1 || report.Summary.ByCommand[0].Count != 1 {
		t.Fatalf("expected by-command aggregation to exclude raw runs, got %+v", report.Summary.ByCommand)
	}
}

func TestLoadReportNormalizesHistoricCommandLabels(t *testing.T) {
	setTrackingEnv(t)

	if err := RecordRun(Record{
		Timestamp:     time.Now(),
		ProjectPath:   "/tmp/project",
		Command:       "gradlew ./gradlew build",
		Mode:          "human",
		RawTokens:     100,
		EmittedTokens: 10,
		SavedTokens:   90,
		SavingsPct:    90,
	}); err != nil {
		t.Fatalf("record run: %v", err)
	}

	report, err := LoadReport("", true)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}

	if len(report.Summary.ByCommand) != 1 || report.Summary.ByCommand[0].Command != "gradlew build" {
		t.Fatalf("expected normalized by-command label, got %+v", report.Summary.ByCommand)
	}
	if len(report.Recent) != 1 || report.Recent[0].Command != "gradlew build" {
		t.Fatalf("expected normalized recent label, got %+v", report.Recent)
	}
}

func TestRecordRunScrubsLegacySensitiveCommandsBeforeRewrite(t *testing.T) {
	setTrackingEnv(t)

	path, err := dbPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}
	legacySecret := "legacy quoted project secret"
	if err := writeRecords(path, []Record{
		{
			Timestamp:     time.Now().Add(-time.Minute),
			ProjectPath:   "/tmp/project",
			Command:       "gradlew test -P 'legacy.project=" + legacySecret + "'",
			Mode:          "human",
			RawTokens:     100,
			EmittedTokens: 10,
			SavedTokens:   90,
			SavingsPct:    90,
		},
	}); err != nil {
		t.Fatalf("seed legacy records: %v", err)
	}

	if err := RecordRun(Record{
		Timestamp:     time.Now(),
		ProjectPath:   "/tmp/project",
		Command:       "gradlew test",
		Mode:          "human",
		RawTokens:     100,
		EmittedTokens: 10,
		SavedTokens:   90,
		SavingsPct:    90,
	}); err != nil {
		t.Fatalf("rewrite tracking records: %v", err)
	}

	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten tracking history: %v", err)
	}
	if strings.Contains(string(stored), legacySecret) {
		t.Fatalf("rewritten tracking history retained legacy secret: %s", stored)
	}
	if !strings.Contains(string(stored), "redacted") {
		t.Fatalf("rewritten tracking history did not retain a redaction marker: %s", stored)
	}
}

func TestRecordRunMigratesKnownSafePredecessorLabels(t *testing.T) {
	setTrackingEnv(t)

	path, err := dbPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}

	legacy := []struct {
		command string
		want    string
	}{
		{
			command: "gradlew :app:assembleDebug --tests com.example.SafeTest -P<redacted>",
			want:    "v2:gradlew :app:assembleDebug --tests com.example.SafeTest -P<redacted>",
		},
		{
			command: "gradlew :app:assembleDebug --tests com.example.SafeTest -P <redacted>",
			want:    "v2:gradlew :app:assembleDebug --tests com.example.SafeTest -P <redacted>",
		},
		{
			command: "gradlew :app:assembleDebug --tests com.example.SafeTest -D<redacted>",
			want:    "v2:gradlew :app:assembleDebug --tests com.example.SafeTest -D<redacted>",
		},
		{
			command: "gradlew :app:assembleDebug --tests com.example.SafeTest -D <redacted>",
			want:    "v2:gradlew :app:assembleDebug --tests com.example.SafeTest -D <redacted>",
		},
		{
			command: "gradlew :app:assembleDebug --tests com.example.SafeTest --project-prop <redacted>",
			want:    "v2:gradlew :app:assembleDebug --tests com.example.SafeTest --project-prop <redacted>",
		},
		{
			command: "gradlew :app:assembleDebug --tests com.example.SafeTest --project-prop=<redacted>",
			want:    "v2:gradlew :app:assembleDebug --tests com.example.SafeTest --project-prop=<redacted>",
		},
		{
			command: "gradlew :app:assembleDebug --tests com.example.SafeTest --system-prop <redacted>",
			want:    "v2:gradlew :app:assembleDebug --tests com.example.SafeTest --system-prop <redacted>",
		},
		{
			command: "gradlew :app:assembleDebug --tests com.example.SafeTest --system-prop=<redacted>",
			want:    "v2:gradlew :app:assembleDebug --tests com.example.SafeTest --system-prop=<redacted>",
		},
	}

	seeded := make([]Record, 0, len(legacy))
	for index, item := range legacy {
		seeded = append(seeded, Record{
			Timestamp:     time.Now().Add(-time.Duration(len(legacy)-index) * time.Minute),
			ProjectPath:   "/tmp/project",
			Command:       item.command,
			Mode:          "human",
			RawTokens:     100,
			EmittedTokens: 10,
			SavedTokens:   90,
			SavingsPct:    90,
		})
	}
	if err := writeRecords(path, seeded); err != nil {
		t.Fatalf("seed legacy records: %v", err)
	}

	if err := RecordRun(Record{
		Timestamp:     time.Now(),
		ProjectPath:   "/tmp/project",
		Command:       "gradlew check",
		Mode:          "human",
		RawTokens:     100,
		EmittedTokens: 10,
		SavedTokens:   90,
		SavingsPct:    90,
	}); err != nil {
		t.Fatalf("rewrite tracking records: %v", err)
	}

	records, err := loadRecords(path)
	if err != nil {
		t.Fatalf("load rewritten tracking history: %v", err)
	}
	if len(records) != len(legacy)+1 {
		t.Fatalf("expected %d rewritten records, got %d", len(legacy)+1, len(records))
	}
	for index, item := range legacy {
		if records[index].Command != item.want {
			t.Fatalf("expected stored command %q, got %q", item.want, records[index].Command)
		}
	}

	report, err := LoadReport("", true)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	for _, item := range legacy {
		want := strings.TrimPrefix(item.want, "v2:")
		found := false
		for _, record := range report.Recent {
			if record.Command == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("report lost migrated safe label %q: %+v", want, report.Recent)
		}
	}
}

func TestRecordRunRedactsUnrecoverableLegacySensitiveLabels(t *testing.T) {
	setTrackingEnv(t)

	path, err := dbPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}

	legacy := []struct {
		command   string
		fragments []string
	}{
		{
			command:   "gradlew test -Pproject.key=pOneSecret pTwoSecret pThreeSecret",
			fragments: []string{"pOneSecret", "pTwoSecret", "pThreeSecret"},
		},
		{
			command:   "gradlew test -Dsystem.key=dOneSecret dTwoSecret dThreeSecret",
			fragments: []string{"dOneSecret", "dTwoSecret", "dThreeSecret"},
		},
		{
			command:   "gradlew test --project-prop project.key=ppOneSecret ppTwoSecret ppThreeSecret",
			fragments: []string{"ppOneSecret", "ppTwoSecret", "ppThreeSecret"},
		},
		{
			command:   "gradlew test --system-prop system.key=spOneSecret spTwoSecret spThreeSecret",
			fragments: []string{"spOneSecret", "spTwoSecret", "spThreeSecret"},
		},
	}

	seeded := make([]Record, 0, len(legacy))
	for index, item := range legacy {
		seeded = append(seeded, Record{
			Timestamp:     time.Now().Add(-time.Duration(index+1) * time.Minute),
			ProjectPath:   "/tmp/project",
			Command:       item.command,
			Mode:          "human",
			RawTokens:     100,
			EmittedTokens: 10,
			SavedTokens:   90,
			SavingsPct:    90,
		})
	}
	if err := writeRecords(path, seeded); err != nil {
		t.Fatalf("seed legacy records: %v", err)
	}

	if err := RecordRun(Record{
		Timestamp:     time.Now(),
		ProjectPath:   "/tmp/project",
		Command:       "gradlew check",
		Mode:          "human",
		RawTokens:     100,
		EmittedTokens: 10,
		SavedTokens:   90,
		SavingsPct:    90,
	}); err != nil {
		t.Fatalf("rewrite tracking records: %v", err)
	}

	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten tracking history: %v", err)
	}
	report, err := LoadReport("", true)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}

	var text bytes.Buffer
	if err := RenderText(&text, report, true); err != nil {
		t.Fatalf("render text: %v", err)
	}
	var jsonOutput bytes.Buffer
	if err := RenderJSON(&jsonOutput, report); err != nil {
		t.Fatalf("render json: %v", err)
	}
	observed := string(stored) + text.String() + jsonOutput.String()
	for _, item := range legacy {
		for _, fragment := range item.fragments {
			if strings.Contains(observed, fragment) {
				t.Fatalf("legacy secret fragment %q leaked: %s", fragment, observed)
			}
		}
	}

	redactedLabels := 0
	for _, record := range report.Recent {
		if record.Command == "<redacted legacy command>" {
			redactedLabels++
		}
	}
	if redactedLabels != len(legacy) {
		t.Fatalf("expected %d redacted legacy labels, got %d: %+v", len(legacy), redactedLabels, report.Recent)
	}
}

func setTrackingEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}
