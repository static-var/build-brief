package tracking

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	retentionDays = 90
	recentLimit   = 15
)

type Record struct {
	Timestamp     time.Time `json:"timestamp"`
	ProjectPath   string    `json:"project_path"`
	Command       string    `json:"command"`
	Mode          string    `json:"mode"`
	Success       bool      `json:"success"`
	RawTokens     int       `json:"raw_tokens"`
	EmittedTokens int       `json:"emitted_tokens"`
	SavedTokens   int       `json:"saved_tokens"`
	SavingsPct    float64   `json:"savings_pct"`
	ExecTimeMs    int64     `json:"exec_time_ms"`
	RawLogPath    string    `json:"raw_log_path,omitempty"`
	FailedTasks   int       `json:"failed_tasks,omitempty"`
	FailedTests   int       `json:"failed_tests,omitempty"`
}

type Summary struct {
	TotalCommands  int                `json:"total_commands"`
	TotalRawTokens int                `json:"total_raw_tokens"`
	TotalEmitted   int                `json:"total_emitted_tokens"`
	TotalSaved     int                `json:"total_saved_tokens"`
	AvgSavingsPct  float64            `json:"avg_savings_pct"`
	TotalTimeMs    int64              `json:"total_time_ms"`
	AvgTimeMs      int64              `json:"avg_time_ms"`
	ByCommand      []CommandAggregate `json:"by_command"`
}

type CommandAggregate struct {
	Command       string  `json:"command"`
	Count         int     `json:"count"`
	SavedTokens   int     `json:"saved_tokens"`
	AvgSavingsPct float64 `json:"avg_savings_pct"`
	AvgTimeMs     int64   `json:"avg_time_ms"`
}

type Report struct {
	ScopeProject string   `json:"scope_project,omitempty"`
	Summary      Summary  `json:"summary"`
	Recent       []Record `json:"recent,omitempty"`
}

func EstimateTokens(text string) int {
	charCount := utf8.RuneCountInString(text)
	if charCount == 0 {
		return 0
	}
	return (charCount + 3) / 4
}

func EstimateFileTokens(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return EstimateTokens(string(content)), nil
}

func SavedTokens(rawTokens, emittedTokens int) int {
	return rawTokens - emittedTokens
}

func SavingsPct(rawTokens, emittedTokens int) float64 {
	if rawTokens <= 0 {
		return 0
	}
	return (float64(rawTokens-emittedTokens) / float64(rawTokens)) * 100
}

func RecordRun(record Record) error {
	path, err := dbPath()
	if err != nil {
		return err
	}

	records, err := loadRecords(path)
	if err != nil {
		return err
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	filtered := make([]Record, 0, len(records)+1)
	for _, existing := range records {
		if existing.Timestamp.After(cutoff) {
			filtered = append(filtered, existing)
		}
	}

	filtered = append(filtered, record)
	return writeRecords(path, filtered)
}

func LoadReport(projectPath string, history bool) (Report, error) {
	path, err := dbPath()
	if err != nil {
		return Report{}, err
	}

	records, err := loadRecords(path)
	if err != nil {
		return Report{}, err
	}

	filtered := make([]Record, 0, len(records))
	for _, record := range records {
		if projectPath != "" && filepath.Clean(record.ProjectPath) != filepath.Clean(projectPath) {
			continue
		}
		filtered = append(filtered, record)
	}

	report := Report{
		ScopeProject: projectPath,
		Summary:      summarize(filtered),
	}

	if history {
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Timestamp.After(filtered[j].Timestamp)
		})
		if len(filtered) > recentLimit {
			filtered = filtered[:recentLimit]
		}
		report.Recent = filtered
	}

	return report, nil
}

func Reset() error {
	path, err := dbPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func RenderText(w io.Writer, report Report, history bool) error {
	if report.Summary.TotalCommands == 0 {
		_, err := fmt.Fprintln(w, "No tracking data yet.")
		return err
	}

	scope := "Global Scope"
	if report.ScopeProject != "" {
		scope = "Project Scope"
	}

	if _, err := fmt.Fprintf(w, "build-brief Token Savings (%s)\n", scope); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, strings.Repeat("=", 60)); err != nil {
		return err
	}
	if report.ScopeProject != "" {
		if _, err := fmt.Fprintf(w, "Scope: %s\n\n", report.ScopeProject); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Total commands:  %s\n", formatCount(report.Summary.TotalCommands)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Raw tokens:      %s\n", FormatTokens(report.Summary.TotalRawTokens)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Emitted tokens:  %s\n", FormatTokens(report.Summary.TotalEmitted)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Tokens saved:    %s (%.1f%%)\n", FormatTokens(report.Summary.TotalSaved), report.Summary.AvgSavingsPct); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Total exec time: %s (avg %s)\n", formatDuration(report.Summary.TotalTimeMs), formatDuration(report.Summary.AvgTimeMs)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Efficiency:      %s %.1f%%\n", efficiencyMeter(report.Summary.AvgSavingsPct), report.Summary.AvgSavingsPct); err != nil {
		return err
	}

	if len(report.Summary.ByCommand) > 0 {
		if _, err := fmt.Fprintln(w, "\nBy Command"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, strings.Repeat("-", 78)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%3s  %-28s  %5s  %8s  %6s  %8s\n", "#", "Command", "Count", "Saved", "Avg%", "Time"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, strings.Repeat("-", 78)); err != nil {
			return err
		}
		for idx, aggregate := range report.Summary.ByCommand {
			if _, err := fmt.Fprintf(
				w,
				"%3d  %-28s  %5d  %8s  %5.1f%%  %8s\n",
				idx+1,
				truncate(aggregate.Command, 28),
				aggregate.Count,
				FormatTokens(aggregate.SavedTokens),
				aggregate.AvgSavingsPct,
				formatDuration(aggregate.AvgTimeMs),
			); err != nil {
				return err
			}
		}
	}

	if history && len(report.Recent) > 0 {
		if _, err := fmt.Fprintln(w, "\nRecent Commands"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, strings.Repeat("-", 58)); err != nil {
			return err
		}
		for _, record := range report.Recent {
			symbol := "•"
			if record.SavedTokens > 0 {
				symbol = "▲"
			} else if record.SavedTokens < 0 {
				symbol = "▼"
			}
			if _, err := fmt.Fprintf(
				w,
				"%s %s %-28s %6.1f%% (%s)\n",
				record.Timestamp.Local().Format("01-02 15:04"),
				symbol,
				truncate(record.Command, 28),
				record.SavingsPct,
				FormatTokens(record.SavedTokens),
			); err != nil {
				return err
			}
		}
	}

	return nil
}

func RenderJSON(w io.Writer, report Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func FormatTokens(tokens int) string {
	value := float64(tokens)
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%s%.1fM", sign, value/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%s%.1fK", sign, value/1_000)
	default:
		return fmt.Sprintf("%s%d", sign, int(value))
	}
}

func summarize(records []Record) Summary {
	summary := Summary{}
	commandStats := map[string]*CommandAggregate{}
	commandTime := map[string]int64{}

	for _, record := range records {
		summary.TotalCommands++
		summary.TotalRawTokens += record.RawTokens
		summary.TotalEmitted += record.EmittedTokens
		summary.TotalSaved += record.SavedTokens
		summary.TotalTimeMs += record.ExecTimeMs

		aggregate := commandStats[record.Command]
		if aggregate == nil {
			aggregate = &CommandAggregate{Command: record.Command}
			commandStats[record.Command] = aggregate
		}
		aggregate.Count++
		aggregate.SavedTokens += record.SavedTokens
		aggregate.AvgSavingsPct += record.SavingsPct
		commandTime[record.Command] += record.ExecTimeMs
	}

	if summary.TotalCommands > 0 {
		summary.AvgTimeMs = summary.TotalTimeMs / int64(summary.TotalCommands)
		summary.AvgSavingsPct = SavingsPct(summary.TotalRawTokens, summary.TotalEmitted)
	}

	summary.ByCommand = make([]CommandAggregate, 0, len(commandStats))
	for _, aggregate := range commandStats {
		aggregate.AvgSavingsPct = aggregate.AvgSavingsPct / float64(aggregate.Count)
		aggregate.AvgTimeMs = commandTime[aggregate.Command] / int64(aggregate.Count)
		summary.ByCommand = append(summary.ByCommand, *aggregate)
	}

	sort.Slice(summary.ByCommand, func(i, j int) bool {
		if summary.ByCommand[i].SavedTokens == summary.ByCommand[j].SavedTokens {
			return summary.ByCommand[i].Command < summary.ByCommand[j].Command
		}
		return summary.ByCommand[i].SavedTokens > summary.ByCommand[j].SavedTokens
	})
	if len(summary.ByCommand) > 10 {
		summary.ByCommand = summary.ByCommand[:10]
	}

	return summary
}

func dbPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "build-brief")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "tracking.jsonl"), nil
}

func loadRecords(path string) ([]Record, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	records := make([]Record, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}

func writeRecords(path string, records []Record) error {
	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(file)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			file.Close()
			return err
		}
	}

	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func formatCount(count int) string {
	return fmt.Sprintf("%d", count)
}

func formatDuration(milliseconds int64) string {
	switch {
	case milliseconds >= 60_000:
		return fmt.Sprintf("%.1fm", float64(milliseconds)/60_000)
	case milliseconds >= 1_000:
		return fmt.Sprintf("%.1fs", float64(milliseconds)/1_000)
	default:
		return fmt.Sprintf("%dms", milliseconds)
	}
}

func efficiencyMeter(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}

	filled := int((pct / 100) * 24)
	if filled < 0 {
		filled = 0
	}
	if filled > 24 {
		filled = 24
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", 24-filled)
}
