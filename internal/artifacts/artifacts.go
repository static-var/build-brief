package artifacts

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxArtifactsReported = 20
	artifactTimeSkew     = time.Second
)

type Artifact struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

type Snapshot struct {
	Captured        bool                     `json:"-"`
	ArtifactEntries map[string]SnapshotEntry `json:"-"`
	ClassEntries    map[string]SnapshotEntry `json:"-"`
	CodegenEntries  map[string]SnapshotEntry `json:"-"`
}

type SnapshotEntry struct {
	ModTimeUnixNano int64 `json:"-"`
	SizeBytes       int64 `json:"-"`
}

type artifactRoot struct {
	parts []string
}

var (
	rootedArtifactHintPattern   = regexp.MustCompile(`(?i)(?:file://)?(?:(?:[A-Za-z]:[\\/])|/|\./|\.\./)[^"'<>]*?(?:\.apk|\.aab|\.aar|\.jar|\.war|\.ear|\.zip|\.tgz|\.tar\.gz|\.tar|\.framework|\.xcframework|\.klib|\.kexe)(?:[\\/][^\s"'<>:,;]+)*`)
	quotedArtifactHintPattern   = regexp.MustCompile(`(?i)["']([^"']+\.(?:apk|aab|aar|jar|war|ear|zip|tgz|tar\.gz|tar|framework|xcframework|klib|kexe)(?:[\\/][^"']*)?)["']`)
	relativeArtifactHintPattern = regexp.MustCompile(`(?i)(?:\.[\\/]|[A-Za-z0-9_.-]+[\\/])[A-Za-z0-9_ ./\\-]*?\.(?:apk|aab|aar|jar|war|ear|zip|tgz|tar\.gz|tar|framework|xcframework|klib|kexe)(?:[\\/][A-Za-z0-9_ ./\\-]*)?`)
)

var artifactRoots = []artifactRoot{
	{parts: []string{"outputs", "apk"}},
	{parts: []string{"outputs", "bundle"}},
	{parts: []string{"outputs", "aar"}},
	{parts: []string{"libs"}},
	{parts: []string{"distributions"}},
	{parts: []string{"bin"}},
	{parts: []string{"XCFrameworks"}},
}

func Capture(projectDir string) Snapshot {
	snapshot := Snapshot{
		Captured:        true,
		ArtifactEntries: make(map[string]SnapshotEntry),
		ClassEntries:    make(map[string]SnapshotEntry),
		CodegenEntries:  make(map[string]SnapshotEntry),
	}

	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if buildDir, ok, skip := buildDirPath(path, entry); ok {
			captureBuildDir(buildDir, projectDir, &snapshot)
			if skip {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if ShouldSkipDir(entry) {
			return filepath.SkipDir
		}

		return nil
	})

	return snapshot
}

func FindGenerated(projectDir string, startedAt time.Time, snapshot Snapshot, hints []string) ([]Artifact, int, int) {
	threshold := startedAt.Add(-artifactTimeSkew)
	artifacts := make([]Artifact, 0)
	seenArtifacts := make(map[string]struct{})
	classCount := 0
	codegenCount := 0

	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if buildDir, ok, skip := buildDirPath(path, entry); ok {
			scanBuildDir(buildDir, projectDir, threshold, snapshot, &artifacts, seenArtifacts, &classCount, &codegenCount)
			if skip {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if ShouldSkipDir(entry) {
			return filepath.SkipDir
		}

		return nil
	})

	for _, hint := range hints {
		addHintArtifact(hint, projectDir, threshold, snapshot, &artifacts, seenArtifacts)
	}

	sortArtifacts(artifacts)
	artifacts = trimArtifacts(artifacts)

	return artifacts, classCount, codegenCount
}

func FindAvailable(projectDir string, hints []string) []Artifact {
	artifacts := make([]Artifact, 0)
	seenArtifacts := make(map[string]struct{})

	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if buildDir, ok, skip := buildDirPath(path, entry); ok {
			scanAvailableBuildDir(buildDir, projectDir, &artifacts, seenArtifacts)
			if skip {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if ShouldSkipDir(entry) {
			return filepath.SkipDir
		}

		return nil
	})

	for _, hint := range hints {
		addAvailableHintArtifact(hint, projectDir, &artifacts, seenArtifacts)
	}

	sortArtifacts(artifacts)
	return trimArtifacts(artifacts)
}

func ExtractHints(text string) []string {
	seen := make(map[string]struct{})
	hints := make([]string, 0)
	for _, match := range rootedArtifactHintPattern.FindAllString(text, -1) {
		addHint(&hints, seen, match)
	}
	for _, match := range relativeArtifactHintPattern.FindAllString(text, -1) {
		addHint(&hints, seen, match)
	}
	for _, groups := range quotedArtifactHintPattern.FindAllStringSubmatch(text, -1) {
		if len(groups) > 1 {
			addHint(&hints, seen, groups[1])
		}
	}
	if len(hints) == 0 {
		return nil
	}
	return hints
}

func ShouldSkipDir(entry fs.DirEntry) bool {
	switch entry.Name() {
	case ".git", ".gradle", ".idea", ".vscode", "node_modules", "buildSrc":
		return true
	default:
		return false
	}
}

func buildDirPath(path string, entry fs.DirEntry) (string, bool, bool) {
	if entry.Name() != "build" {
		return "", false, false
	}
	if entry.IsDir() {
		return path, true, true
	}
	if entry.Type()&fs.ModeSymlink == 0 {
		return "", false, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", false, false
	}
	return path, true, false
}

func captureBuildDir(buildDir, projectDir string, snapshot *Snapshot) {
	for _, root := range artifactRoots {
		captureArtifactRoot(filepath.Join(append([]string{buildDir}, root.parts...)...), projectDir, snapshot.ArtifactEntries)
	}

	captureMatchingFiles(filepath.Join(buildDir, "classes"), projectDir, snapshot.ClassEntries, func(path string) bool {
		return strings.HasSuffix(strings.ToLower(path), ".class")
	})
	captureMatchingFiles(filepath.Join(buildDir, "tmp", "kotlin-classes"), projectDir, snapshot.ClassEntries, func(path string) bool {
		return strings.HasSuffix(strings.ToLower(path), ".class")
	})
	captureMatchingFiles(filepath.Join(buildDir, "generated"), projectDir, snapshot.CodegenEntries, func(path string) bool {
		return true
	})
}

func scanBuildDir(buildDir, projectDir string, threshold time.Time, snapshot Snapshot, artifacts *[]Artifact, seenArtifacts map[string]struct{}, classCount, codegenCount *int) {
	for _, root := range artifactRoots {
		scanArtifactRoot(filepath.Join(append([]string{buildDir}, root.parts...)...), projectDir, threshold, snapshot, artifacts, seenArtifacts)
	}

	*classCount += countChangedFiles(filepath.Join(buildDir, "classes"), projectDir, threshold, snapshot.Captured, snapshot.ClassEntries, func(path string) bool {
		return strings.HasSuffix(strings.ToLower(path), ".class")
	})
	*classCount += countChangedFiles(filepath.Join(buildDir, "tmp", "kotlin-classes"), projectDir, threshold, snapshot.Captured, snapshot.ClassEntries, func(path string) bool {
		return strings.HasSuffix(strings.ToLower(path), ".class")
	})
	*codegenCount += countChangedFiles(filepath.Join(buildDir, "generated"), projectDir, threshold, snapshot.Captured, snapshot.CodegenEntries, func(path string) bool {
		return true
	})
}

func scanAvailableBuildDir(buildDir, projectDir string, artifacts *[]Artifact, seenArtifacts map[string]struct{}) {
	for _, root := range artifactRoots {
		scanAvailableArtifactRoot(filepath.Join(append([]string{buildDir}, root.parts...)...), projectDir, artifacts, seenArtifacts)
	}
}

func captureArtifactRoot(rootDir, projectDir string, entries map[string]SnapshotEntry) {
	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		return
	}

	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			artifact, state, ok := buildArtifact(path, entry, projectDir)
			if ok {
				entries[artifact.Path] = state
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		artifact, state, ok := buildArtifact(path, entry, projectDir)
		if !ok {
			return nil
		}
		entries[artifact.Path] = state
		return nil
	})
}

func scanArtifactRoot(rootDir, projectDir string, threshold time.Time, snapshot Snapshot, artifacts *[]Artifact, seenArtifacts map[string]struct{}) {
	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		return
	}

	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			artifact, state, ok := buildArtifact(path, entry, projectDir)
			if ok && shouldReportState(artifact.Path, state, threshold, snapshot.Captured, snapshot.ArtifactEntries) {
				if addArtifact(artifact, artifacts, seenArtifacts) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		artifact, state, ok := buildArtifact(path, entry, projectDir)
		if !ok || !shouldReportState(artifact.Path, state, threshold, snapshot.Captured, snapshot.ArtifactEntries) {
			return nil
		}
		addArtifact(artifact, artifacts, seenArtifacts)
		return nil
	})
}

func captureMatchingFiles(rootDir, projectDir string, entries map[string]SnapshotEntry, match func(path string) bool) {
	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		return
	}

	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !match(filepath.ToSlash(path)) {
			return nil
		}

		relativePath, state, ok := buildTrackedFile(path, entry, projectDir)
		if !ok {
			return nil
		}
		entries[relativePath] = state
		return nil
	})
}

func countChangedFiles(rootDir, projectDir string, threshold time.Time, useSnapshot bool, beforeEntries map[string]SnapshotEntry, match func(path string) bool) int {
	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		return 0
	}

	count := 0
	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !match(filepath.ToSlash(path)) {
			return nil
		}

		relativePath, state, ok := buildTrackedFile(path, entry, projectDir)
		if !ok || !shouldReportState(relativePath, state, threshold, useSnapshot, beforeEntries) {
			return nil
		}
		count++
		return nil
	})

	return count
}

func addHintArtifact(hint, projectDir string, threshold time.Time, snapshot Snapshot, artifacts *[]Artifact, seenArtifacts map[string]struct{}) {
	resolvedPath, ok := resolveArtifactHintPath(projectDir, hint)
	if !ok {
		return
	}

	entry, err := os.Lstat(resolvedPath)
	if err != nil || entry.Mode()&fs.ModeSymlink != 0 {
		return
	}

	dirEntry := fs.FileInfoToDirEntry(entry)
	artifact, state, ok := buildArtifact(resolvedPath, dirEntry, projectDir)
	if !ok {
		return
	}
	if !shouldReportHintState(artifact.Path, state, threshold, snapshot) {
		return
	}
	addArtifact(artifact, artifacts, seenArtifacts)
}

func addAvailableHintArtifact(hint, projectDir string, artifacts *[]Artifact, seenArtifacts map[string]struct{}) {
	resolvedPath, ok := resolveArtifactHintPath(projectDir, hint)
	if !ok {
		return
	}

	entry, err := os.Lstat(resolvedPath)
	if err != nil || entry.Mode()&fs.ModeSymlink != 0 {
		return
	}

	dirEntry := fs.FileInfoToDirEntry(entry)
	artifact, _, ok := buildArtifact(resolvedPath, dirEntry, projectDir)
	if !ok {
		return
	}
	addArtifact(artifact, artifacts, seenArtifacts)
}

func buildArtifact(path string, entry fs.DirEntry, projectDir string) (Artifact, SnapshotEntry, bool) {
	info, err := entry.Info()
	if err != nil {
		return Artifact{}, SnapshotEntry{}, false
	}

	kind, ok := artifactKind(entry.Name(), entry.IsDir())
	if !ok {
		return Artifact{}, SnapshotEntry{}, false
	}

	relativePath, err := filepath.Rel(projectDir, path)
	if err != nil {
		relativePath = path
	}

	state := SnapshotEntry{
		ModTimeUnixNano: info.ModTime().UnixNano(),
		SizeBytes:       info.Size(),
	}
	if entry.IsDir() {
		state = directorySnapshot(path, info.ModTime())
	}

	return Artifact{
		Kind:      kind,
		Path:      filepath.ToSlash(relativePath),
		SizeBytes: state.SizeBytes,
	}, state, true
}

func buildTrackedFile(path string, entry fs.DirEntry, projectDir string) (string, SnapshotEntry, bool) {
	info, err := entry.Info()
	if err != nil {
		return "", SnapshotEntry{}, false
	}

	relativePath, err := filepath.Rel(projectDir, path)
	if err != nil {
		relativePath = path
	}

	return filepath.ToSlash(relativePath), SnapshotEntry{
		ModTimeUnixNano: info.ModTime().UnixNano(),
		SizeBytes:       info.Size(),
	}, true
}

func shouldReportState(path string, current SnapshotEntry, threshold time.Time, useSnapshot bool, beforeEntries map[string]SnapshotEntry) bool {
	if useSnapshot {
		before, ok := beforeEntries[path]
		return !ok || before != current
	}
	return modifiedSince(current.ModTimeUnixNano, threshold)
}

// Hints can point outside standard roots, so when a snapshot exists we still
// fall back to the mtime gate for hinted paths that were not present before.
func shouldReportHintState(path string, current SnapshotEntry, threshold time.Time, snapshot Snapshot) bool {
	if snapshot.Captured {
		if before, ok := snapshot.ArtifactEntries[path]; ok {
			return before != current
		}
	}
	return modifiedSince(current.ModTimeUnixNano, threshold)
}

func modifiedSince(modTimeUnixNano int64, threshold time.Time) bool {
	return !time.Unix(0, modTimeUnixNano).Before(threshold)
}

func addArtifact(artifact Artifact, artifacts *[]Artifact, seenArtifacts map[string]struct{}) bool {
	if _, ok := seenArtifacts[artifact.Path]; ok {
		return false
	}
	seenArtifacts[artifact.Path] = struct{}{}
	*artifacts = append(*artifacts, artifact)
	return true
}

func scanAvailableArtifactRoot(rootDir, projectDir string, artifacts *[]Artifact, seenArtifacts map[string]struct{}) {
	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		return
	}

	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			artifact, _, ok := buildArtifact(path, entry, projectDir)
			if ok && addArtifact(artifact, artifacts, seenArtifacts) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		artifact, _, ok := buildArtifact(path, entry, projectDir)
		if !ok {
			return nil
		}
		addArtifact(artifact, artifacts, seenArtifacts)
		return nil
	})
}

func sortArtifacts(artifacts []Artifact) {
	sort.Slice(artifacts, func(i, j int) bool {
		priorityI := artifactPriority(artifacts[i])
		priorityJ := artifactPriority(artifacts[j])
		if priorityI != priorityJ {
			return priorityI < priorityJ
		}
		if artifacts[i].SizeBytes != artifacts[j].SizeBytes {
			return artifacts[i].SizeBytes > artifacts[j].SizeBytes
		}
		if artifacts[i].Kind != artifacts[j].Kind {
			return artifacts[i].Kind < artifacts[j].Kind
		}
		return artifacts[i].Path < artifacts[j].Path
	})
}

func trimArtifacts(artifacts []Artifact) []Artifact {
	if len(artifacts) > maxArtifactsReported {
		return artifacts[:maxArtifactsReported]
	}
	return artifacts
}

func artifactKind(name string, isDir bool) (string, bool) {
	lower := strings.ToLower(name)
	if isDir {
		switch {
		case strings.HasSuffix(lower, ".framework"):
			return "FRAMEWORK", true
		case strings.HasSuffix(lower, ".xcframework"):
			return "XCFRAMEWORK", true
		default:
			return "", false
		}
	}

	switch {
	case strings.HasSuffix(lower, ".apk"):
		return "APK", true
	case strings.HasSuffix(lower, ".aab"):
		return "AAB", true
	case strings.HasSuffix(lower, ".aar"):
		return "AAR", true
	case strings.HasSuffix(lower, ".jar"):
		return "JAR", true
	case strings.HasSuffix(lower, ".war"):
		return "WAR", true
	case strings.HasSuffix(lower, ".ear"):
		return "EAR", true
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar"):
		return "TAR", true
	case strings.HasSuffix(lower, ".zip"):
		return "ZIP", true
	case strings.HasSuffix(lower, ".klib"):
		return "KLIB", true
	case strings.HasSuffix(lower, ".kexe"):
		return "KEXE", true
	default:
		return "", false
	}
}

func artifactPriority(artifact Artifact) int {
	switch artifact.Kind {
	case "APK":
		return 0
	case "AAB":
		return 1
	case "XCFRAMEWORK":
		return 2
	case "FRAMEWORK":
		return 3
	case "ZIP", "TAR", "WAR", "EAR", "KEXE":
		return 4
	case "JAR":
		return 5
	case "KLIB":
		return 6
	case "AAR":
		return 7
	default:
		return 8
	}
}

func addHint(hints *[]string, seen map[string]struct{}, raw string) {
	hint := normalizeArtifactHint(raw)
	if hint == "" {
		return
	}
	if _, ok := seen[hint]; ok {
		return
	}
	seen[hint] = struct{}{}
	*hints = append(*hints, hint)
}

func normalizeArtifactHint(hint string) string {
	hint = strings.TrimSpace(hint)
	hint = strings.TrimPrefix(hint, "file://")
	hint = strings.Trim(hint, `"'()[]{}<>`)
	hint = strings.TrimRight(hint, ".,;:")
	hint = strings.TrimRight(hint, `/\`)
	hint = trimDirectoryArtifactSuffix(hint)
	return hint
}

func trimDirectoryArtifactSuffix(hint string) string {
	lower := strings.ToLower(hint)
	for _, ext := range []string{".xcframework", ".framework"} {
		index := strings.Index(lower, ext)
		if index < 0 {
			continue
		}
		end := index + len(ext)
		if end == len(hint) {
			return hint
		}
		next := hint[end]
		if next == '/' || next == '\\' {
			return hint[:end]
		}
	}
	return hint
}

func resolveArtifactHintPath(projectDir, hint string) (string, bool) {
	if hint == "" {
		return "", false
	}

	path := hint
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, path)
	}

	absoluteProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return "", false
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	relativePath, err := filepath.Rel(absoluteProjectDir, absolutePath)
	if err != nil {
		return "", false
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", false
	}

	return absolutePath, true
}

func directorySnapshot(rootDir string, initialModTime time.Time) SnapshotEntry {
	summary := SnapshotEntry{
		ModTimeUnixNano: initialModTime.UnixNano(),
	}
	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		summary.SizeBytes += info.Size()
		if modTime := info.ModTime().UnixNano(); modTime > summary.ModTimeUnixNano {
			summary.ModTimeUnixNano = modTime
		}
		return nil
	})
	return summary
}
