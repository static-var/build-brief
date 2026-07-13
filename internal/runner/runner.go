package runner

import (
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

// AncillaryError marks a failure in post-execution maintenance, not Gradle execution or output capture.
type AncillaryError struct {
	Err error
}

func (e *AncillaryError) Error() string {
	return e.Err.Error()
}

func (e *AncillaryError) Unwrap() error {
	return e.Err
}

// IsAncillaryError reports whether err is a post-execution ancillary failure.
func IsAncillaryError(err error) bool {
	var ancillaryErr *AncillaryError
	return errors.As(err, &ancillaryErr)
}

type ProgressEvent struct {
	RawLogPath string
	Elapsed    time.Duration
}

type Options struct {
	Progress         func(ProgressEvent)
	ProgressInterval time.Duration
}

const (
	maxProjectRawLogs = 20
	commandWaitDelay  = 5 * time.Second
)

type rawLogWriter struct {
	destination io.Writer
	onError     func()

	mu  sync.Mutex
	err error
}

func (w *rawLogWriter) Write(p []byte) (int, error) {
	n, err := w.destination.Write(p)
	if err == nil {
		return n, nil
	}

	w.mu.Lock()
	firstError := w.err == nil
	if firstError {
		w.err = err
	}
	w.mu.Unlock()

	if firstError && w.onError != nil {
		w.onError()
	}
	return n, err
}

func (w *rawLogWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

type outputCaptureResult struct {
	err                   error
	timedOut              bool
	startedByCancellation bool
}

func startOutputCapture(ctx context.Context, processDone <-chan struct{}, reader *os.File, destination io.Writer) <-chan outputCaptureResult {
	copyDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(destination, reader)
		copyDone <- err
	}()

	result := make(chan outputCaptureResult, 1)
	go func() {
		startedByCancellation := false
		select {
		case <-ctx.Done():
			startedByCancellation = true
		case <-processDone:
		}

		timer := time.NewTimer(commandWaitDelay)
		defer timer.Stop()

		select {
		case err := <-copyDone:
			result <- outputCaptureResult{
				err:                   err,
				startedByCancellation: startedByCancellation,
			}
		case <-timer.C:
			_ = reader.Close()
			result <- outputCaptureResult{
				err:                   <-copyDone,
				timedOut:              true,
				startedByCancellation: startedByCancellation,
			}
		}
	}()

	return result
}

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
	artifactSnapshot := artifacts.Snapshot{}
	if !gradle.AnalyzeArgs(command.Args).IsPureInformational {
		artifactSnapshot = artifacts.Capture(command.ProjectDir)
	}

	captureReader, captureWriter, err := os.Pipe()
	if err != nil {
		return Result{RawLogPath: rawLogPath, ArtifactSnapshot: artifactSnapshot}, fmt.Errorf("create output capture pipe: %w", err)
	}
	defer func() {
		_ = captureReader.Close()
		_ = captureWriter.Close()
	}()

	cmd := exec.CommandContext(runCtx, command.Executable, command.Args...)
	cmd.Dir = command.ProjectDir
	configureCommand(cmd)
	// WaitDelay bounds cancellation and descendants that retain inherited
	// output descriptors after the command exits. The capture reader below
	// applies the same bound while keeping truncation observable.
	cmd.WaitDelay = commandWaitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := interruptProcess(cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}

	// Both streams use one shared pipe writer. The reader below owns the one
	// combined capture stream, preserving the current stdout/stderr ordering
	// while keeping its lifecycle observable outside os/exec.
	outputWriter := &rawLogWriter{
		destination: rawLogFile,
		onError:     cancel,
	}
	cmd.Stdout = captureWriter
	cmd.Stderr = captureWriter

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{RawLogPath: rawLogPath, ArtifactSnapshot: artifactSnapshot}, fmt.Errorf("start gradle command: %w", err)
	}

	processDone := make(chan struct{})
	captureResultCh := startOutputCapture(runCtx, processDone, captureReader, outputWriter)
	var captureWriterCloseErr error
	if err := captureWriter.Close(); err != nil {
		captureWriterCloseErr = fmt.Errorf("close output capture: %w", err)
	}

	doneCh := make(chan struct{})
	startProgressReporter(rawLogPath, startedAt, opts, doneCh)
	defer close(doneCh)

	waitErr := cmd.Wait()
	close(processDone)
	captureResult := <-captureResultCh
	duration := time.Since(startedAt)
	exitCode := exitCodeFromWait(waitErr, cmd)

	writeErr := outputWriter.Err()
	var closeErr error
	if err := rawLogFile.Close(); err != nil {
		closeErr = fmt.Errorf("close raw log: %w", err)
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
		return result, fmt.Errorf("write raw log: %w", writeErr)
	}

	if closeErr != nil {
		return result, closeErr
	}

	if captureWriterCloseErr != nil {
		return result, captureWriterCloseErr
	}

	if waitErr != nil {
		var exitErr *exec.ExitError
		if !os.IsTimeout(waitErr) && !errors.As(waitErr, &exitErr) {
			return result, fmt.Errorf("wait for gradle command: %w", waitErr)
		}
	}

	if !captureResult.startedByCancellation {
		if captureResult.timedOut {
			return result, fmt.Errorf("capture gradle output: %w", exec.ErrWaitDelay)
		}
		if captureResult.err != nil {
			return result, fmt.Errorf("capture gradle output: %w", captureResult.err)
		}
	}

	if err := pruneProjectRawLogs(filepath.Dir(rawLogPath), command.ProjectDir, rawLogPath); err != nil {
		return result, &AncillaryError{Err: fmt.Errorf("prune raw log files: %w", err)}
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
