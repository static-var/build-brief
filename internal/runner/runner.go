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
	"sync"
	"time"

	"build-brief/internal/gradle"
)

type Result struct {
	ExitCode   int           `json:"exit_code"`
	Duration   time.Duration `json:"duration"`
	RawLogPath string        `json:"raw_log_path"`
}

func Run(ctx context.Context, command gradle.Command, logDir string) (Result, error) {
	rawLogPath, rawLogFile, err := newRawLogFile(logDir, command.ProjectDir)
	if err != nil {
		return Result{}, fmt.Errorf("create raw log file: %w", err)
	}
	defer rawLogFile.Close()

	cmd := exec.CommandContext(ctx, command.Executable, command.Args...)
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
		return Result{RawLogPath: rawLogPath}, fmt.Errorf("start gradle command: %w", err)
	}

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

	for line := range linesCh {
		if _, err := fmt.Fprintln(rawLogFile, line); err != nil {
			return Result{RawLogPath: rawLogPath}, fmt.Errorf("write raw log: %w", err)
		}
	}

	for scanErr := range scanErrCh {
		if scanErr != nil {
			return Result{RawLogPath: rawLogPath}, fmt.Errorf("scan command output: %w", scanErr)
		}
	}

	waitErr := cmd.Wait()
	duration := time.Since(startedAt)
	exitCode := exitCodeFromWait(waitErr, cmd)

	if waitErr != nil {
		var exitErr *exec.ExitError
		if !os.IsTimeout(waitErr) && !errors.As(waitErr, &exitErr) {
			return Result{
				ExitCode:   exitCode,
				Duration:   duration,
				RawLogPath: rawLogPath,
			}, fmt.Errorf("wait for gradle command: %w", waitErr)
		}
	}

	return Result{
		ExitCode:   exitCode,
		Duration:   duration,
		RawLogPath: rawLogPath,
	}, nil
}

func newRawLogFile(logDir, projectDir string) (string, *os.File, error) {
	if logDir == "" {
		logDir = filepath.Join(os.TempDir(), "build-brief")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", nil, err
	}

	fileName := fmt.Sprintf("build-brief-%08x.latest.log", projectHash(projectDir))
	rawLogPath := filepath.Join(logDir, fileName)
	file, err := os.OpenFile(rawLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", nil, err
	}

	return rawLogPath, file, nil
}

func scanStream(stream io.ReadCloser, linesCh chan<- string, errCh chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		linesCh <- scanner.Text()
	}

	errCh <- scanner.Err()
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
