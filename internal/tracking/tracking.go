package tracking

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"build-brief/internal/gradle"
)

const (
	retentionDays        = 90
	localGainsDisclosure = "Local-only: Gains history stays on this machine. This report sends no gains data."
	recentLimit          = 15
	lockTimeout          = 5 * time.Second
	lockPollInterval     = 25 * time.Millisecond
	staleLockAge         = 30 * time.Second
)

var (
	createTrackingTemp   = os.CreateTemp
	renameTrackingFile   = os.Rename
	encodeTrackingRecord = func(encoder *json.Encoder, record Record) error { return encoder.Encode(record) }
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
	ExecTimeMs    int64     `json:"-"`
	RawLogPath    string    `json:"raw_log_path,omitempty"`
	FailedTasks   int       `json:"failed_tasks,omitempty"`
	PassedTests   int       `json:"passed_tests,omitempty"`
	FailedTests   int       `json:"failed_tests,omitempty"`
}

type Summary struct {
	TotalCommands  int                `json:"total_commands"`
	TotalRawTokens int                `json:"total_raw_tokens"`
	TotalEmitted   int                `json:"total_emitted_tokens"`
	TotalSaved     int                `json:"total_saved_tokens"`
	AvgSavingsPct  float64            `json:"avg_savings_pct"`
	ByCommand      []CommandAggregate `json:"by_command"`
}

type CommandAggregate struct {
	Command       string  `json:"command"`
	Count         int     `json:"count"`
	SavedTokens   int     `json:"saved_tokens"`
	AvgSavingsPct float64 `json:"avg_savings_pct"`
}

type Report struct {
	ScopeProject   string   `json:"scope_project,omitempty"`
	Summary        Summary  `json:"summary"`
	Recent         []Record `json:"recent,omitempty"`
	recordedPeriod period
}

type period struct {
	start time.Time
	end   time.Time
	count int
}

type lockMetadata struct {
	PID       int
	CreatedAt time.Time
	Token     string
}

type lockHandle struct {
	file  *os.File
	token string
}

func EstimateTokens(text string) int {
	charCount := utf8.RuneCountInString(text)
	if charCount == 0 {
		return 0
	}
	return (charCount + 3) / 4
}

func EstimateReaderTokens(r io.Reader) (int, error) {
	reader := bufio.NewReaderSize(r, 64*1024)
	runeCount := 0
	for {
		_, _, err := reader.ReadRune()
		if err == nil {
			runeCount++
			continue
		}
		if err == io.EOF {
			break
		}
		return 0, err
	}

	if runeCount == 0 {
		return 0, nil
	}
	return (runeCount + 3) / 4, nil
}

func EstimateFileTokens(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	return EstimateReaderTokens(file)
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
	record.Command = gradle.SanitizeTrackingLine(record.Command)
	path, err := dbPath()
	if err != nil {
		return err
	}

	return withTrackingLock(path, func() error {
		records, err := loadRecords(path)
		if err != nil {
			return err
		}

		cutoff := time.Now().AddDate(0, 0, -retentionDays)
		filtered := make([]Record, 0, len(records)+1)
		for _, existing := range records {
			if existing.Timestamp.After(cutoff) {
				existing.Command = gradle.SanitizeTrackingLine(existing.Command)
				filtered = append(filtered, existing)
			}
		}

		filtered = append(filtered, record)
		return writeRecords(path, filtered)
	})
}

func LoadReport(projectPath string, history bool) (Report, error) {
	path, err := trackingPath()
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
		if strings.EqualFold(record.Mode, "raw") {
			continue
		}
		record.Command = normalizeHistoricCommand(record.Command)
		filtered = append(filtered, record)
	}

	report := Report{
		ScopeProject:   projectPath,
		Summary:        summarize(filtered),
		recordedPeriod: periodFor(filtered),
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
	return withTrackingLock(path, func() error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

func RenderText(w io.Writer, report Report, history bool) error {
	report = sanitizeReport(report)
	if report.Summary.TotalCommands == 0 {
		if _, err := fmt.Fprintln(w, "No gains data yet."); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w, localGainsDisclosure)
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

	if _, err := fmt.Fprintf(w, "Recorded period: %s\n", report.recordedPeriod.String()); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
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
		if _, err := fmt.Fprintf(w, "%3s  %-28s  %5s  %8s  %6s\n", "#", "Command", "Count", "Saved", "Avg%"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, strings.Repeat("-", 78)); err != nil {
			return err
		}
		for idx, aggregate := range report.Summary.ByCommand {
			if _, err := fmt.Fprintf(
				w,
				"%3d  %-28s  %5d  %8s  %5.1f%%\n",
				idx+1,
				truncate(aggregate.Command, 28),
				aggregate.Count,
				FormatTokens(aggregate.SavedTokens),
				aggregate.AvgSavingsPct,
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

	_, err := fmt.Fprintln(w, "\n"+localGainsDisclosure)
	return err
}

func RenderJSON(w io.Writer, report Report) error {
	report = sanitizeReport(report)
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

func periodFor(records []Record) period {
	var result period
	for _, record := range records {
		if record.Timestamp.IsZero() {
			continue
		}
		date := record.Timestamp.Local()
		date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.Local)
		if result.count == 0 || date.Before(result.start) {
			result.start = date
		}
		if result.count == 0 || date.After(result.end) {
			result.end = date
		}
		result.count++
	}
	return result
}

func (value period) String() string {
	if value.count == 0 {
		return "unavailable"
	}

	start := time.Date(value.start.Year(), value.start.Month(), value.start.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(value.end.Year(), value.end.Month(), value.end.Day(), 0, 0, 0, 0, time.UTC)
	days := int(end.Sub(start).Hours()/24) + 1
	dayLabel := "days"
	if days == 1 {
		dayLabel = "day"
	}
	commandLabel := "commands"
	if value.count == 1 {
		commandLabel = "command"
	}
	dates := value.start.Format("2006-01-02")
	if !value.start.Equal(value.end) {
		dates += " to " + value.end.Format("2006-01-02")
	}
	return fmt.Sprintf("%s (%d %s, %d %s)", dates, days, dayLabel, value.count, commandLabel)
}

func summarize(records []Record) Summary {
	summary := Summary{}
	commandStats := map[string]*CommandAggregate{}

	for _, record := range records {
		summary.TotalCommands++
		summary.TotalRawTokens += record.RawTokens
		summary.TotalEmitted += record.EmittedTokens
		summary.TotalSaved += record.SavedTokens

		aggregate := commandStats[record.Command]
		if aggregate == nil {
			aggregate = &CommandAggregate{Command: record.Command}
			commandStats[record.Command] = aggregate
		}
		aggregate.Count++
		aggregate.SavedTokens += record.SavedTokens
		aggregate.AvgSavingsPct += record.SavingsPct
	}

	if summary.TotalCommands > 0 {
		summary.AvgSavingsPct = SavingsPct(summary.TotalRawTokens, summary.TotalEmitted)
	}

	summary.ByCommand = make([]CommandAggregate, 0, len(commandStats))
	for _, aggregate := range commandStats {
		aggregate.AvgSavingsPct = aggregate.AvgSavingsPct / float64(aggregate.Count)
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

func sanitizeReport(report Report) Report {
	for i := range report.Summary.ByCommand {
		report.Summary.ByCommand[i].Command = gradle.SanitizeHistoricCommand(report.Summary.ByCommand[i].Command)
	}
	for i := range report.Recent {
		report.Recent[i].Command = gradle.SanitizeHistoricCommand(report.Recent[i].Command)
	}
	return report
}

func normalizeHistoricCommand(command string) string {
	fields := strings.Fields(gradle.SanitizeHistoricCommand(command))
	if len(fields) == 0 {
		return ""
	}

	normalized := make([]string, 0, len(fields))
	normalized = append(normalized, fields[0])
	for _, field := range fields[1:] {
		switch filepath.Base(strings.ReplaceAll(field, `\`, `/`)) {
		case "gradle", "gradlew", "gradlew.bat":
			continue
		default:
			normalized = append(normalized, field)
		}
	}

	return strings.Join(normalized, " ")
}

func dbPath() (string, error) {
	path, err := trackingPath()
	if err != nil {
		return "", err
	}
	if err := ensureTrackingDir(filepath.Dir(path)); err != nil {
		return "", err
	}
	return path, nil
}

// trackingPath resolves the history location without creating or repairing it.
func trackingPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "build-brief", "tracking.jsonl"), nil
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

func withTrackingLock(path string, fn func() error) (err error) {
	lockPath := path + ".lock"
	lockFile, err := acquireLockFile(lockPath, lockTimeout)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, releaseLockFile(lockPath, lockFile))
	}()

	return fn()
}

func acquireLockFile(lockPath string, timeout time.Duration) (*lockHandle, error) {
	deadline := time.Now().Add(timeout)
	for {
		guard, err := acquireReclaimGuard(lockPath, deadline)
		if err != nil {
			return nil, err
		}

		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			handle := &lockHandle{file: file, token: newLockToken()}
			writeErr := writeLockMetadata(handle)
			guardErr := releaseReclaimGuard(guard)
			if writeErr == nil && guardErr == nil {
				return handle, nil
			}
			cleanupErr := releaseLockFile(lockPath, handle)
			return nil, errors.Join(writeErr, guardErr, cleanupErr)
		}
		if !os.IsExist(err) {
			guardErr := releaseReclaimGuard(guard)
			return nil, errors.Join(err, guardErr)
		}
		if err := rejectSymlink(lockPath, "tracking lock"); err != nil {
			guardErr := releaseReclaimGuard(guard)
			return nil, errors.Join(err, guardErr)
		}

		reclaimed, reclaimErr := reclaimStaleLockUnderGuard(lockPath)
		guardErr := releaseReclaimGuard(guard)
		if reclaimErr != nil || guardErr != nil {
			return nil, errors.Join(reclaimErr, guardErr)
		}
		if reclaimed {
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("tracking file is locked: %s", lockPath)
		}
		time.Sleep(lockPollInterval)
	}
}

// reclaimStaleLock serializes stale-lock verification and removal with every
// lock creator. A successor cannot be created between the final verification
// and removal because creators must hold the same reclaim guard.
func reclaimStaleLock(lockPath string, timeout time.Duration) (bool, error) {
	guard, err := acquireReclaimGuard(lockPath, time.Now().Add(timeout))
	if err != nil {
		return false, err
	}
	reclaimed, reclaimErr := reclaimStaleLockUnderGuard(lockPath)
	guardErr := releaseReclaimGuard(guard)
	return reclaimed, errors.Join(reclaimErr, guardErr)
}

func reclaimStaleLockUnderGuard(lockPath string) (bool, error) {
	if !shouldBreakStaleLock(lockPath) {
		return false, nil
	}
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("remove stale tracking lock %s: %w", lockPath, err)
	}
	return true, nil
}

func acquireReclaimGuard(lockPath string, deadline time.Time) (*os.File, error) {
	guardPath := reclaimGuardPath(lockPath)
	if err := rejectSymlink(guardPath, "tracking lock reclaim guard"); err != nil {
		return nil, err
	}
	guard, err := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockReclaimGuard(guard, deadline); err != nil {
		return nil, errors.Join(err, guard.Close())
	}
	return guard, nil
}

func releaseReclaimGuard(guard *os.File) error {
	return errors.Join(unlockReclaimGuard(guard), guard.Close())
}

func reclaimGuardPath(lockPath string) string {
	return lockPath + ".reclaim"
}

func newLockToken() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(fmt.Sprintf("generate tracking lock token: %v", err))
	}
	return hex.EncodeToString(bytes[:])
}

func writeLockMetadata(handle *lockHandle) error {
	_, err := fmt.Fprintf(handle.file, "pid=%d\ncreated_at=%s\ntoken=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano), handle.token)
	return err
}

func shouldBreakStaleLock(lockPath string) bool {
	metadata, err := readLockMetadata(lockPath)
	if err != nil {
		return fileOlderThan(lockPath, staleLockAge)
	}

	if metadata.PID > 0 {
		known, alive := processLiveness(metadata.PID)
		if known {
			return !alive
		}
	}

	if !metadata.CreatedAt.IsZero() && time.Since(metadata.CreatedAt) >= staleLockAge {
		return true
	}
	return fileOlderThan(lockPath, staleLockAge)
}

func readLockMetadata(lockPath string) (lockMetadata, error) {
	content, err := os.ReadFile(lockPath)
	if err != nil {
		return lockMetadata{}, err
	}

	var metadata lockMetadata
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "pid":
			pid, err := strconv.Atoi(value)
			if err != nil {
				return lockMetadata{}, err
			}
			metadata.PID = pid
		case "created_at":
			createdAt, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return lockMetadata{}, err
			}
			metadata.CreatedAt = createdAt
		case "token":
			metadata.Token = value
		}
	}
	return metadata, nil
}

func fileOlderThan(path string, age time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) >= age
}

func releaseLockFile(lockPath string, handle *lockHandle) error {
	if handle == nil {
		return nil
	}

	guard, err := acquireReclaimGuard(lockPath, time.Now().Add(lockTimeout))
	if err != nil {
		return errors.Join(err, handle.file.Close())
	}
	return errors.Join(handle.file.Close(), releaseLockOwnershipUnderGuard(lockPath, handle.token, guard))
}

// releaseLockOwnership removes a lock only when it still belongs to token.
func releaseLockOwnership(lockPath, token string) error {
	guard, err := acquireReclaimGuard(lockPath, time.Now().Add(lockTimeout))
	if err != nil {
		return err
	}
	return releaseLockOwnershipUnderGuard(lockPath, token, guard)
}

func releaseLockOwnershipUnderGuard(lockPath, token string, guard *os.File) error {
	metadata, readErr := readLockMetadata(lockPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return releaseReclaimGuard(guard)
		}
		return errors.Join(readErr, releaseReclaimGuard(guard))
	}
	if metadata.Token != token {
		return releaseReclaimGuard(guard)
	}
	return errors.Join(removeLockFile(lockPath, lockTimeout, os.Remove), releaseReclaimGuard(guard))
}

// removeLockFile retries a failed removal because Windows does not permit
// deleting a file while a competing stale-lock check has it open.
func removeLockFile(lockPath string, timeout time.Duration, remove func(string) error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := remove(lockPath)
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("remove tracking lock %s: %w", lockPath, err)
		}
		time.Sleep(lockPollInterval)
	}
}

func writeRecords(path string, records []Record) (err error) {
	file, err := createTrackingTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	closed := false
	cleanup := true
	defer func() {
		if cleanup {
			if !closed {
				err = errors.Join(err, file.Close())
			}
			err = errors.Join(err, os.Remove(tmpPath))
		}
	}()

	if err := file.Chmod(0o600); err != nil {
		return err
	}

	encoder := json.NewEncoder(file)
	for _, record := range records {
		if err := encodeTrackingRecord(encoder, record); err != nil {
			return err
		}
	}

	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	closed = true
	if err := renameTrackingFile(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func rejectSymlink(path, description string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink: %s", description, path)
	}
	return nil
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
