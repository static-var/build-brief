package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"build-brief/internal/artifacts"
	"build-brief/internal/gradle"
)

type Result struct {
	ExitCode         int                `json:"exit_code"`
	Duration         time.Duration      `json:"duration"`
	StartTime        time.Time          `json:"start_time"`
	RawLogPath       string             `json:"raw_log_path"`
	ArtifactSnapshot artifacts.Snapshot `json:"-"`
}

type ProgressEvent struct {
	RawLogPath string
	Elapsed    time.Duration
}

type Options struct {
	Progress         func(ProgressEvent)
	ProgressInterval time.Duration
}

const maxProjectRawLogs = 20

func Run(ctx context.Context, command gradle.Command, logDir string) (Result, error) {
	return RunWithOptions(ctx, command, logDir, Options{})
}

func RunWithOptions(ctx context.Context, command gradle.Command, logDir string, opts Options) (Result, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	rawLogPath, rawLogFile, err := newRawLogFile(logDir, command.ProjectDir)
	if err != nil {
		return Result{}, fmt.Errorf("create raw log file: %w", err)
	}
	rawLogClosed := false
	defer func() {
		if !rawLogClosed {
			_ = rawLogFile.Close()
		}
	}()
	artifactSnapshot := artifacts.Capture(command.ProjectDir)

	cmd := exec.CommandContext(runCtx, command.Executable, command.Args...)
	cmd.Dir = command.ProjectDir
	configureCommand(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := interruptProcess(cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{RawLogPath: rawLogPath}, fmt.Errorf("attach stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{RawLogPath: rawLogPath}, fmt.Errorf("attach stderr pipe: %w", err)
	}

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{RawLogPath: rawLogPath, ArtifactSnapshot: artifactSnapshot}, fmt.Errorf("start gradle command: %w", err)
	}

	doneCh := make(chan struct{})
	startProgressReporter(rawLogPath, startedAt, opts, doneCh)
	defer close(doneCh)

	linesCh := make(chan string, 64)
	scanErrCh := make(chan error, 2)
	var readers sync.WaitGroup
	readers.Add(2)

	go scanStream(stdout, linesCh, scanErrCh, &readers)
	go scanStream(stderr, linesCh, scanErrCh, &readers)
	go func() {
		readers.Wait()
		close(linesCh)
		close(scanErrCh)
	}()

	var writeErr error
	for line := range linesCh {
		if writeErr != nil {
			continue
		}
		if _, err := fmt.Fprintln(rawLogFile, line); err != nil {
			writeErr = fmt.Errorf("write raw log: %w", err)
			cancel()
		}
	}

	var streamErr error
	for scanErr := range scanErrCh {
		if scanErr != nil && streamErr == nil {
			streamErr = fmt.Errorf("scan command output: %w", scanErr)
			cancel()
		}
	}

	waitErr := cmd.Wait()
	duration := time.Since(startedAt)
	exitCode := exitCodeFromWait(waitErr, cmd)

	if err := rawLogFile.Close(); err != nil && writeErr == nil {
		writeErr = fmt.Errorf("close raw log: %w", err)
	}
	rawLogClosed = true

	result := Result{
		ExitCode:         exitCode,
		Duration:         duration,
		StartTime:        startedAt,
		RawLogPath:       rawLogPath,
		ArtifactSnapshot: artifactSnapshot,
	}

	finalRawLogPath, err := finalizeRawLogFile(rawLogPath)
	if err != nil {
		return result, fmt.Errorf("finalize raw log file: %w", err)
	}
	rawLogPath = finalRawLogPath

	result.RawLogPath = rawLogPath

	if writeErr != nil {
		return result, writeErr
	}

	if streamErr != nil {
		return result, streamErr
	}

	if waitErr != nil {
		var exitErr *exec.ExitError
		if !os.IsTimeout(waitErr) && !errors.As(waitErr, &exitErr) {
			return result, fmt.Errorf("wait for gradle command: %w", waitErr)
		}
	}

	if err := pruneProjectRawLogs(filepath.Dir(rawLogPath), command.ProjectDir, rawLogPath); err != nil {
		return result, fmt.Errorf("prune raw log files: %w", err)
	}

	return result, nil
}

func startProgressReporter(rawLogPath string, startedAt time.Time, opts Options, done <-chan struct{}) {
	if opts.Progress == nil || opts.ProgressInterval <= 0 {
		return
	}

	ticker := time.NewTicker(opts.ProgressInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				opts.Progress(ProgressEvent{
					RawLogPath: rawLogPath,
					Elapsed:    time.Since(startedAt),
				})
			}
		}
	}()
}

func newRawLogFile(logDir, projectDir string) (string, *os.File, error) {
	if logDir == "" {
		logDir = filepath.Join(os.TempDir(), "build-brief")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", nil, err
	}

	file, err := os.CreateTemp(logDir, fmt.Sprintf("build-brief-%08x-*.partial.log", projectHash(projectDir)))
	if err != nil {
		return "", nil, err
	}

	return file.Name(), file, nil
}

func finalizeRawLogFile(path string) (string, error) {
	if !strings.HasSuffix(path, ".partial.log") {
		return path, nil
	}

	finalPath := strings.TrimSuffix(path, ".partial.log") + ".log"
	if err := os.Rename(path, finalPath); err != nil {
		return "", err
	}

	return finalPath, nil
}

func pruneProjectRawLogs(logDir, projectDir, keepPath string) error {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return err
	}

	prefix := fmt.Sprintf("build-brief-%08x-", projectHash(projectDir))
	type candidate struct {
		path    string
		modTime time.Time
	}

	candidates := make([]candidate, 0, len(entries))
	currentIncluded := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".partial.log") {
			continue
		}

		path := filepath.Join(logDir, name)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		candidateEntry := candidate{
			path:    path,
			modTime: info.ModTime(),
		}
		if path == keepPath {
			currentIncluded = true
			continue
		}
		candidates = append(candidates, candidateEntry)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].path > candidates[j].path
		}
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	keepOthers := maxProjectRawLogs
	if currentIncluded && keepOthers > 0 {
		keepOthers--
	}

	for i, candidate := range candidates {
		if i < keepOthers {
			continue
		}
		if err := os.Remove(candidate.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

func scanStream(stream io.ReadCloser, linesCh chan<- string, errCh chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	defer stream.Close()

	reader := bufio.NewReaderSize(stream, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			linesCh <- trimLineEnding(line)
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			errCh <- nil
			return
		}
		errCh <- err
		return
	}
}

func trimLineEnding(line string) string {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line
}

func exitCodeFromWait(waitErr error, cmd *exec.Cmd) int {
	if code, ok := signalExitCode(waitErr); ok {
		return code
	}

	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}

	if waitErr == nil {
		return 0
	}

	return 1
}

func projectHash(projectDir string) uint32 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(projectDir))
	return hasher.Sum32()
}
