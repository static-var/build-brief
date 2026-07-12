//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunKeepsRequiredRawLogFinalizationFailureFatal(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()
	t.Setenv("BUILD_BRIEF_TEST_LOG_DIR", logDir)
	defer func() { _ = os.Chmod(logDir, 0o755) }()

	scriptPath := filepath.Join(t.TempDir(), "fake-gradle.sh")
	script := "#!/bin/sh\necho 'BUILD SUCCESSFUL in 1s'\nchmod 0555 \"$BUILD_BRIEF_TEST_LOG_DIR\"\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gradle: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		"--log-dir", logDir,
		"test",
	}, strings.NewReader(""), &stdout, &stderr)

	if exitCode == 0 {
		t.Fatalf("expected required raw-log finalization failure to remain fatal, stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "finalize raw log file") {
		t.Fatalf("expected finalization failure to be visible, got stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Raw log:") {
		t.Fatalf("expected raw log path to be visible, got stderr=%q", stderr.String())
	}
}
