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
	maxScanErrors        = 8
	artifactTimeSkew     = time.Second
)

type Artifact struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

type ScanMetadata struct {
	Discovered      int      `json:"discovered"`
	Reported        int      `json:"reported"`
	Skipped         int      `json:"skipped"`
	Errors          []string `json:"errors,omitempty"`
	ErrorCount      int      `json:"error_count,omitempty"`
	ErrorsTruncated bool     `json:"errors_truncated,omitempty"`
	Truncated       bool     `json:"truncated"`
}

type GeneratedResult struct {
	Artifacts    []Artifact
	ClassCount   int
	CodegenCount int
	Metadata     ScanMetadata
}

type AvailableResult struct {
	Artifacts []Artifact
	Metadata  ScanMetadata
}

type artifactCollector struct {
	artifacts  []Artifact
	projectDir string
	scope      []string
	metadata   ScanMetadata
}

func newArtifactCollector(projectDir string) *artifactCollector {
	return &artifactCollector{
		artifacts:  make([]Artifact, 0, maxArtifactsReported),
		projectDir: projectDir,
	}
}

func newScopedArtifactCollector(projectDir string, prefixes []string) *artifactCollector {
	collector := newArtifactCollector(projectDir)
	for _, prefix := range prefixes {
		prefix = strings.ReplaceAll(strings.TrimSpace(prefix), `\`, "/")
		prefix = filepath.ToSlash(strings.Trim(prefix, "/"))
		if prefix != "" {
			collector.scope = append(collector.scope, prefix)
		}
	}
	return collector
}

func (c *artifactCollector) add(artifact Artifact) bool {
	// Dedupe is bounded to retained top-K entries; discarded candidates are
	// counted but never retained, so the collector state cannot grow with the scan.
	if !c.matchesScope(artifact) {
		// Artifact directories can be pruned even when they are out of scope.
		return true
	}
	for _, retained := range c.artifacts {
		if retained.Path == artifact.Path {
			return false
		}
	}

	c.metadata.Discovered++
	if len(c.artifacts) < maxArtifactsReported {
		c.artifacts = append(c.artifacts, artifact)
		sortArtifacts(c.artifacts)
		return true
	}

	c.metadata.Truncated = true
	if artifactLess(artifact, c.artifacts[maxArtifactsReported-1]) {
		c.artifacts[maxArtifactsReported-1] = artifact
		sortArtifacts(c.artifacts)
	}
	return true
}

func (c *artifactCollector) matchesScope(artifact Artifact) bool {
	if len(c.scope) == 0 {
		return true
	}
	path := filepath.ToSlash(strings.Trim(artifact.Path, "/"))
	for _, prefix := range c.scope {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (c *artifactCollector) addError(path string, err error) {
	if err == nil {
		return
	}
	c.metadata.ErrorCount++
	if len(c.metadata.Errors) >= maxScanErrors {
		c.metadata.ErrorsTruncated = true
		return
	}
	scanError := sanitizeScanErrorText(c.projectDir, err.Error())
	c.metadata.Errors = append(c.metadata.Errors, relativeScanPath(c.projectDir, path)+": "+scanError)
}

func (c *artifactCollector) finish() ScanMetadata {
	sortArtifacts(c.artifacts)
	c.metadata.Reported = len(c.artifacts)
	c.metadata.Skipped = c.metadata.Discovered - c.metadata.Reported
	if c.metadata.Skipped > 0 {
		c.metadata.Truncated = true
	}
	return c.metadata
}

func relativeScanPath(projectDir, path string) string {
	if relative, err := filepath.Rel(projectDir, path); err == nil {
		return filepath.ToSlash(relative)
	}
	return sanitizeScanErrorText(projectDir, filepath.ToSlash(path))
}

func sanitizeScanErrorText(projectDir, text string) string {
	for _, root := range scanPathVariants(projectDir) {
		text = strings.ReplaceAll(text, root, "<project>")
	}
	return text
}

func scanPathVariants(projectDir string) []string {
	if projectDir == "" {
		return nil
	}
	candidates := []string{projectDir, filepath.Clean(projectDir)}
	if absolute, err := filepath.Abs(projectDir); err == nil {
		candidates = append(candidates, absolute, filepath.Clean(absolute))
	}
	variants := make([]string, 0, len(candidates)*2)
	seen := make(map[string]struct{}, len(candidates)*2)
	for _, candidate := range candidates {
		for _, variant := range []string{candidate, filepath.ToSlash(candidate)} {
			if variant == "" {
				continue
			}
			if _, ok := seen[variant]; ok {
				continue
			}
			seen[variant] = struct{}{}
			variants = append(variants, variant)
		}
	}
	return variants
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
	result := FindGeneratedWithMetadata(projectDir, startedAt, snapshot, hints)
	return result.Artifacts, result.ClassCount, result.CodegenCount
}

func FindGeneratedWithMetadata(projectDir string, startedAt time.Time, snapshot Snapshot, hints []string) GeneratedResult {
	threshold := startedAt.Add(-artifactTimeSkew)
	collector := newArtifactCollector(projectDir)
	classCount := 0
	codegenCount := 0

	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			collector.addError(path, walkErr)
			return nil
		}
		if buildDir, ok, skip := buildDirPath(path, entry); ok {
			scanBuildDir(buildDir, projectDir, threshold, snapshot, collector, &classCount, &codegenCount)
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
		addHintArtifact(hint, projectDir, threshold, snapshot, collector)
	}

	metadata := collector.finish()
	return GeneratedResult{
		Artifacts:    collector.artifacts,
		ClassCount:   classCount,
		CodegenCount: codegenCount,
		Metadata:     metadata,
	}
}

func FindAvailable(projectDir string, hints []string) []Artifact {
	return FindAvailableWithMetadata(projectDir, hints).Artifacts
}

func FindAvailableWithMetadata(projectDir string, hints []string) AvailableResult {
	return FindAvailableScopedWithMetadata(projectDir, hints, nil)
}

func FindAvailableScopedWithMetadata(projectDir string, hints, projectPrefixes []string) AvailableResult {
	collector := newScopedArtifactCollector(projectDir, projectPrefixes)

	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			collector.addError(path, walkErr)
			return nil
		}
		if buildDir, ok, skip := buildDirPath(path, entry); ok {
			scanAvailableBuildDir(buildDir, projectDir, collector)
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
		addAvailableHintArtifact(hint, projectDir, collector)
	}

	metadata := collector.finish()
	return AvailableResult{Artifacts: collector.artifacts, Metadata: metadata}
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

func scanBuildDir(buildDir, projectDir string, threshold time.Time, snapshot Snapshot, collector *artifactCollector, classCount, codegenCount *int) {
	for _, root := range artifactRoots {
		scanArtifactRoot(filepath.Join(append([]string{buildDir}, root.parts...)...), projectDir, threshold, snapshot, collector)
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

func scanAvailableBuildDir(buildDir, projectDir string, collector *artifactCollector) {
	for _, root := range artifactRoots {
		scanAvailableArtifactRoot(filepath.Join(append([]string{buildDir}, root.parts...)...), projectDir, collector)
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

func scanArtifactRoot(rootDir, projectDir string, threshold time.Time, snapshot Snapshot, collector *artifactCollector) {
	info, err := os.Stat(rootDir)
	if err != nil {
		if !os.IsNotExist(err) {
			collector.addError(rootDir, err)
		}
		return
	}
	if !info.IsDir() {
		return
	}

	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			collector.addError(path, walkErr)
			return nil
		}
		if entry.IsDir() {
			artifact, state, ok := buildArtifact(path, entry, projectDir)
			if ok && shouldReportState(artifact.Path, state, threshold, snapshot.Captured, snapshot.ArtifactEntries) {
				if collector.add(artifact) {
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
		collector.add(artifact)
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

func addHintArtifact(hint, projectDir string, threshold time.Time, snapshot Snapshot, collector *artifactCollector) {
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
	collector.add(artifact)
}

func addAvailableHintArtifact(hint, projectDir string, collector *artifactCollector) {
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
	collector.add(artifact)
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

func scanAvailableArtifactRoot(rootDir, projectDir string, collector *artifactCollector) {
	info, err := os.Stat(rootDir)
	if err != nil {
		if !os.IsNotExist(err) {
			collector.addError(rootDir, err)
		}
		return
	}
	if !info.IsDir() {
		return
	}

	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			collector.addError(path, walkErr)
			return nil
		}
		if entry.IsDir() {
			artifact, _, ok := buildArtifact(path, entry, projectDir)
			if ok && collector.add(artifact) {
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
		collector.add(artifact)
		return nil
	})
}

func sortArtifacts(artifacts []Artifact) {
	sort.Slice(artifacts, func(i, j int) bool {
		return artifactLess(artifacts[i], artifacts[j])
	})
}

func artifactLess(left, right Artifact) bool {
	priorityLeft := artifactPriority(left)
	priorityRight := artifactPriority(right)
	if priorityLeft != priorityRight {
		return priorityLeft < priorityRight
	}
	if left.SizeBytes != right.SizeBytes {
		return left.SizeBytes > right.SizeBytes
	}
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	return left.Path < right.Path
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
