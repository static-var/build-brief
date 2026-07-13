package artifacts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureAndFindGeneratedUsesSnapshotDiff(t *testing.T) {
	projectDir := t.TempDir()

	writeFile(t, filepath.Join(projectDir, "app", "build", "libs", "existing.jar"), "old")
	writeFile(t, filepath.Join(projectDir, "app", "build", "classes", "kotlin", "main", "Old.class"), "old-class")
	snapshot := Capture(projectDir)
	startTime := time.Now()

	writeFile(t, filepath.Join(projectDir, "app", "build", "outputs", "apk", "debug", "app-debug.apk"), "apk")
	writeFile(t, filepath.Join(projectDir, "app", "build", "generated", "ksp", "main", "kotlin", "Generated.kt"), "generated")

	found, classCount, codegenCount := FindGenerated(projectDir, startTime, snapshot, nil)

	if !containsArtifact(found, "APK", "app/build/outputs/apk/debug/app-debug.apk") {
		t.Fatalf("expected new apk artifact in %+v", found)
	}
	if containsArtifact(found, "JAR", "app/build/libs/existing.jar") {
		t.Fatalf("did not expect unchanged jar in %+v", found)
	}
	if classCount != 0 {
		t.Fatalf("expected unchanged class files to be omitted, got %d", classCount)
	}
	if codegenCount != 1 {
		t.Fatalf("expected one generated codegen file, got %d", codegenCount)
	}
}

func TestFindGeneratedUsesVerifiedHintFallbackForCustomOutputs(t *testing.T) {
	projectDir := t.TempDir()
	snapshot := Capture(projectDir)
	startTime := time.Now()

	customPath := filepath.Join(projectDir, "custom-output", "Fancy.xcframework", "ios-arm64", "Fancy.framework", "Fancy")
	writeFile(t, customPath, "xcframework")

	found, _, _ := FindGenerated(projectDir, startTime, snapshot, []string{
		filepath.Join(projectDir, "custom-output", "Fancy.xcframework"),
	})

	if !containsArtifact(found, "XCFRAMEWORK", "custom-output/Fancy.xcframework") {
		t.Fatalf("expected custom hinted xcframework in %+v", found)
	}
}

func TestFindGeneratedWithSnapshotDiffExcludesArtifactThatMtimeFallbackWouldInclude(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(projectDir, "app", "build", "libs", "existing.jar")
	writeFile(t, path, "jar")

	snapshot := Capture(projectDir)
	entry, ok := snapshot.ArtifactEntries["app/build/libs/existing.jar"]
	if !ok {
		t.Fatalf("expected snapshot entry for existing artifact, got %+v", snapshot.ArtifactEntries)
	}
	startTime := time.Unix(0, entry.ModTimeUnixNano).Add(500 * time.Millisecond)

	withSnapshot, _, _ := FindGenerated(projectDir, startTime, snapshot, nil)
	withoutSnapshot, _, _ := FindGenerated(projectDir, startTime, Snapshot{}, nil)

	if containsArtifact(withSnapshot, "JAR", "app/build/libs/existing.jar") {
		t.Fatalf("did not expect unchanged artifact with snapshot diff: %+v", withSnapshot)
	}
	if !containsArtifact(withoutSnapshot, "JAR", "app/build/libs/existing.jar") {
		t.Fatalf("expected mtime-only fallback to include artifact, got %+v", withoutSnapshot)
	}
}

func TestFindGeneratedReportsArtifactScanTruncation(t *testing.T) {
	projectDir := t.TempDir()
	for i := 0; i < maxArtifactsReported+1; i++ {
		writeFile(t, filepath.Join(projectDir, "app", "build", "libs", fmt.Sprintf("artifact-%03d.jar", i)), "jar")
	}

	result := FindGeneratedWithMetadata(projectDir, time.Now().Add(-time.Hour), Snapshot{}, nil)
	if len(result.Artifacts) != maxArtifactsReported {
		t.Fatalf("expected capped artifacts, got %d", len(result.Artifacts))
	}
	if result.Metadata.Discovered != maxArtifactsReported+1 || result.Metadata.Reported != maxArtifactsReported {
		t.Fatalf("unexpected artifact scan counts: %+v", result.Metadata)
	}
	if result.Metadata.Skipped != 1 || !result.Metadata.Truncated {
		t.Fatalf("expected one skipped artifact and truncation, got %+v", result.Metadata)
	}
}

func TestArtifactCollectorSanitizesProjectDirFromScanError(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(projectDir, "app", "build", "outputs", "apk")
	collector := newArtifactCollector(projectDir)

	collector.addError(path, &fs.PathError{Op: "open", Path: path, Err: errors.New("permission denied")})
	metadata := collector.finish()

	if len(metadata.Errors) != 1 {
		t.Fatalf("expected one scan error, got %+v", metadata)
	}
	if strings.Contains(metadata.Errors[0], projectDir) {
		t.Fatalf("scan error leaked project directory %q: %q", projectDir, metadata.Errors[0])
	}
}

func TestArtifactCollectorRetainsBoundedStateForLargeCandidateStream(t *testing.T) {
	collector := newArtifactCollector("")
	const candidateCount = 100_000

	for i := 0; i < candidateCount; i++ {
		collector.add(Artifact{Kind: "JAR", Path: fmt.Sprintf("module/build/libs/artifact-%06d.jar", i)})
	}

	if len(collector.artifacts) != maxArtifactsReported {
		t.Fatalf("expected retained artifacts bounded at %d, got %d", maxArtifactsReported, len(collector.artifacts))
	}
	discovered := collector.metadata.Discovered
	collector.add(collector.artifacts[0])
	if collector.metadata.Discovered != discovered {
		t.Fatalf("expected retained artifact duplicate to be ignored, got discovered=%d", collector.metadata.Discovered)
	}
	metadata := collector.finish()
	if metadata.Discovered != candidateCount || metadata.Reported != maxArtifactsReported || metadata.Skipped != candidateCount-maxArtifactsReported || !metadata.Truncated {
		t.Fatalf("unexpected large-stream metadata: %+v", metadata)
	}
}

func TestFindGeneratedPreservesArtifactPriorityWhenCapped(t *testing.T) {
	projectDir := t.TempDir()
	for i := 0; i < maxArtifactsReported; i++ {
		writeFile(t, filepath.Join(projectDir, "app", "build", "libs", fmt.Sprintf("artifact-%03d.jar", i)), "jar")
	}
	writeFile(t, filepath.Join(projectDir, "app", "build", "libs", "zzz-release.apk"), "apk")

	result := FindGeneratedWithMetadata(projectDir, time.Now().Add(-time.Hour), Snapshot{}, nil)
	if len(result.Artifacts) != maxArtifactsReported {
		t.Fatalf("expected capped artifacts, got %d", len(result.Artifacts))
	}
	if !containsArtifact(result.Artifacts, "APK", "app/build/libs/zzz-release.apk") {
		t.Fatalf("expected high-priority apk to survive cap, got %+v", result.Artifacts)
	}
	if result.Metadata.Discovered != maxArtifactsReported+1 || result.Metadata.Reported != maxArtifactsReported || result.Metadata.Skipped != 1 || !result.Metadata.Truncated {
		t.Fatalf("unexpected mixed-priority metadata: %+v", result.Metadata)
	}
}

func TestFindAvailableIncludesExistingArtifactsWithoutSnapshotDiff(t *testing.T) {
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, "androidApp", "build", "outputs", "apk", "debug", "androidApp-debug.apk"), "apk")
	writeFile(t, filepath.Join(projectDir, "server", "build", "libs", "server.jar"), "jar")

	found := FindAvailable(projectDir, nil)

	if !containsArtifact(found, "APK", "androidApp/build/outputs/apk/debug/androidApp-debug.apk") {
		t.Fatalf("expected available apk artifact in %+v", found)
	}
	if !containsArtifact(found, "JAR", "server/build/libs/server.jar") {
		t.Fatalf("expected available jar artifact in %+v", found)
	}
}

func TestExtractHintsSupportsPathsWithSpaces(t *testing.T) {
	line := `Built artifact at "/Users/dev/My Projects/demo/custom output/Fancy.xcframework" and ./relative path/app-debug.apk`
	hints := ExtractHints(line)

	if !containsHint(hints, "/Users/dev/My Projects/demo/custom output/Fancy.xcframework") {
		t.Fatalf("expected absolute hinted path with spaces, got %v", hints)
	}
	if !containsHint(hints, "./relative path/app-debug.apk") {
		t.Fatalf("expected relative hinted path with spaces, got %v", hints)
	}
}

func TestExtractHintsTrimsNestedDirectoryArtifactPaths(t *testing.T) {
	line := `XCFramework assembled at /tmp/Shared.xcframework/Info.plist, verified checksum`
	hints := ExtractHints(line)

	if !containsHint(hints, "/tmp/Shared.xcframework") {
		t.Fatalf("expected xcframework root hint, got %v", hints)
	}
	if containsHint(hints, "/tmp/Shared.xcframework/Info.plist") {
		t.Fatalf("did not expect nested plist path hint, got %v", hints)
	}
}

func TestCaptureSupportsSymlinkedBuildDir(t *testing.T) {
	projectDir := t.TempDir()
	targetBuildDir := filepath.Join(projectDir, "real-build")
	writeFile(t, filepath.Join(targetBuildDir, "libs", "app.jar"), "jar")

	linkPath := filepath.Join(projectDir, "app", "build")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}
	if err := os.Symlink(targetBuildDir, linkPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	snapshot := Capture(projectDir)
	if _, ok := snapshot.ArtifactEntries["app/build/libs/app.jar"]; !ok {
		t.Fatalf("expected snapshot to include artifact from symlinked build dir, got %+v", snapshot.ArtifactEntries)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func containsArtifact(artifacts []Artifact, kind, path string) bool {
	for _, artifact := range artifacts {
		if artifact.Kind == kind && artifact.Path == path {
			return true
		}
	}
	return false
}

func containsHint(hints []string, want string) bool {
	for _, hint := range hints {
		if hint == want {
			return true
		}
	}
	return false
}
