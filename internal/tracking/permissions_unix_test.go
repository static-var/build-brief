//go:build !windows

package tracking

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordRunRewritesBroadDataWithoutRepairingDirectory(t *testing.T) {
	setTrackingEnv(t)
	dir := trackingDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create broad tracking directory: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("set broad tracking directory mode: %v", err)
	}
	path := filepath.Join(dir, "tracking.jsonl")
	history, err := json.Marshal(Record{Timestamp: time.Now().Add(-time.Minute), Command: "gradlew history"})
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	if err := os.WriteFile(path, append(history, '\n'), 0o644); err != nil {
		t.Fatalf("write broad history: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("set broad history mode: %v", err)
	}

	if err := RecordRun(Record{Timestamp: time.Now(), Command: "gradlew test"}); err != nil {
		t.Fatalf("record run: %v", err)
	}

	assertMode(t, dir, 0o755)
	assertMode(t, path, 0o600)
	assertMode(t, path+".lock.reclaim", 0o600)
	if _, err := os.Lstat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary tracking file remains: %v", err)
	}
	if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("tracking lock remains: %v", err)
	}
}

func TestLoadReportDoesNotCreateTrackingDirectory(t *testing.T) {
	setTrackingEnv(t)
	path, err := trackingPath()
	if err != nil {
		t.Fatalf("tracking path: %v", err)
	}
	if _, err := LoadReport("", false); err != nil {
		t.Fatalf("load missing report: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("load report created tracking directory: %v", err)
	}
}

func TestTrackingFilesCreatedPrivate(t *testing.T) {
	setTrackingEnv(t)
	path, err := dbPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}
	if err := RecordRun(Record{Timestamp: time.Now(), Command: "gradlew test"}); err != nil {
		t.Fatalf("record run: %v", err)
	}
	assertMode(t, filepath.Dir(path), 0o700)
	assertMode(t, path, 0o600)
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
}

func TestTrackingSymlinksDoNotMutateTargets(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		setTrackingEnv(t)
		dir := trackingDir(t)
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			t.Fatalf("create config directory: %v", err)
		}
		targetDir := filepath.Join(t.TempDir(), "target")
		if err := os.Mkdir(targetDir, 0o751); err != nil {
			t.Fatalf("create target directory: %v", err)
		}
		if err := os.Chmod(targetDir, 0o751); err != nil {
			t.Fatalf("set target directory mode: %v", err)
		}
		target := filepath.Join(targetDir, "sentinel")
		writeSentinel(t, target)
		if err := os.Symlink(targetDir, dir); err != nil {
			t.Fatalf("symlink tracking directory: %v", err)
		}

		if err := RecordRun(Record{Timestamp: time.Now(), Command: "gradlew test"}); err == nil {
			t.Fatal("record run succeeded through symlinked directory")
		}
		assertMode(t, targetDir, 0o751)
		assertSentinel(t, target)
	})

	t.Run("data load is non-mutating and rewrite replaces link", func(t *testing.T) {
		setTrackingEnv(t)
		path, err := dbPath()
		if err != nil {
			t.Fatalf("db path: %v", err)
		}
		target := filepath.Join(t.TempDir(), "data-target")
		writeSentinel(t, target)
		if err := os.Symlink(target, path); err != nil {
			t.Fatalf("symlink tracking data: %v", err)
		}

		if _, err := LoadReport("", false); err != nil {
			t.Fatalf("load symlinked data: %v", err)
		}
		assertSentinel(t, target)
		if err := RecordRun(Record{Timestamp: time.Now(), Command: "gradlew test"}); err != nil {
			t.Fatalf("rewrite symlinked data: %v", err)
		}
		assertSentinel(t, target)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("stat rewritten data: %v", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("atomic rewrite retained data symlink")
		}
		assertMode(t, path, 0o600)
	})

	for _, suffix := range []string{".tmp", ".lock", ".lock.reclaim"} {
		t.Run(suffix, func(t *testing.T) {
			setTrackingEnv(t)
			path, err := dbPath()
			if err != nil {
				t.Fatalf("db path: %v", err)
			}
			if err := os.WriteFile(path, []byte("history\n"), 0o600); err != nil {
				t.Fatalf("seed history: %v", err)
			}
			target := filepath.Join(t.TempDir(), "target")
			writeSentinel(t, target)
			if err := os.Symlink(target, path+suffix); err != nil {
				t.Fatalf("symlink %s: %v", suffix, err)
			}

			if err := RecordRun(Record{Timestamp: time.Now(), Command: "gradlew test"}); err == nil {
				t.Fatalf("record run succeeded with symlinked %s", suffix)
			}
			assertSentinel(t, target)
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read preserved history: %v", err)
			}
			if string(contents) != "history\n" {
				t.Fatalf("history changed after %s failure: %q", suffix, contents)
			}
		})
	}
}

func trackingDir(t *testing.T) string {
	t.Helper()
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("user config directory: %v", err)
	}
	return filepath.Join(configDir, "build-brief")
}

func writeSentinel(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("unrelated content\n"), 0o640); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("set sentinel mode: %v", err)
	}
}

func assertSentinel(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(contents) != "unrelated content\n" {
		t.Fatalf("sentinel content changed: %q", contents)
	}
	assertMode(t, path, 0o640)
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
