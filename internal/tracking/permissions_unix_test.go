//go:build !windows

package tracking

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrackingFilesAndDirectoryArePrivate(t *testing.T) {
	setTrackingEnv(t)

	path, err := dbPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}
	history, err := json.Marshal(Record{Timestamp: time.Now().Add(-time.Minute), Command: "gradlew history"})
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	if err := os.WriteFile(path, append(history, '\n'), 0o644); err != nil {
		t.Fatalf("write broad history: %v", err)
	}

	if err := RecordRun(Record{Timestamp: time.Now(), Command: "gradlew test"}); err != nil {
		t.Fatalf("record run: %v", err)
	}

	assertMode(t, filepath.Dir(path), 0o700)
	assertMode(t, path, 0o600)
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary tracking file remains: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("tracking lock remains: %v", err)
	}
	assertMode(t, path+".lock.reclaim", 0o600)
}

func TestTrackingLockAndReclaimGuardArePrivate(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "tracking.jsonl.lock")
	lock, err := acquireLockFile(lockPath, time.Second)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	assertMode(t, lockPath, 0o600)
	assertMode(t, reclaimGuardPath(lockPath), 0o600)
	if err := releaseLockFile(lockPath, lock); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("tracking lock remains: %v", err)
	}
	assertMode(t, reclaimGuardPath(lockPath), 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %o, want %o", path, got, want)
	}
}
