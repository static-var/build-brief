package artifacts

import (
	"container/heap"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxArtifactsReported       = 20
	maxScanErrors              = 8
	maxScanCoverageRoots       = 64
	maxScanHints               = maxScanCoverageRoots
	maxScanHintBytes           = 64 * 1024
	maxHintsPerLine            = 64
	maxHintBytesPerLine        = 64 * 1024
	maxHintLength              = 4096
	maxScanErrorBytes          = 64 * 1024
	maxSnapshotArtifactEntries = 4096
	maxSnapshotClassEntries    = 16384
	maxSnapshotCodegenEntries  = 16384
	artifactTimeSkew           = time.Second
)

type Artifact struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

// ScanMetadata describes a bounded artifact scan. Discovered counts accepted
// candidate events from the filesystem and non-standard hints; the filesystem
// traversal emits each path once. Hints
// under standard roots are classified as duplicates and never add another
// discovery event, so standard-root scan metadata remains an exact unique-path
// count even when a retained candidate is later evicted. Non-standard hints
// share only the bounded retained-path ledger. Reported is the retained top-K
// set and Skipped is Discovered-Reported. HintsTruncated distinguishes a
// bounded direct-API hint input from a reporting-cap truncation.
type ScanMetadata struct {
	Discovered        int      `json:"discovered"`
	Reported          int      `json:"reported"`
	Skipped           int      `json:"skipped"`
	Errors            []string `json:"errors,omitempty"`
	ErrorCount        int      `json:"error_count,omitempty"`
	ErrorBytes        int64    `json:"error_bytes,omitempty"`
	ErrorsTruncated   bool     `json:"errors_truncated,omitempty"`
	HintsTruncated    bool     `json:"hints_truncated,omitempty"`
	SnapshotTruncated bool     `json:"snapshot_truncated,omitempty"`
	Truncated         bool     `json:"truncated"`
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

// MergeScanMetadata combines sequential scans while preserving bounded error
// details and every incompleteness signal from both scans.
func MergeScanMetadata(left, right ScanMetadata) ScanMetadata {
	merged := ScanMetadata{
		Discovered:        left.Discovered + right.Discovered,
		Reported:          left.Reported + right.Reported,
		Skipped:           left.Skipped + right.Skipped,
		ErrorCount:        left.ErrorCount + right.ErrorCount,
		HintsTruncated:    left.HintsTruncated || right.HintsTruncated,
		SnapshotTruncated: left.SnapshotTruncated || right.SnapshotTruncated,
	}
	for _, scan := range []ScanMetadata{left, right} {
		for _, scanError := range scan.Errors {
			if len(merged.Errors) >= maxScanErrors || merged.ErrorBytes+int64(len(scanError)) > maxScanErrorBytes {
				merged.ErrorsTruncated = true
				continue
			}
			merged.Errors = append(merged.Errors, scanError)
			merged.ErrorBytes += int64(len(scanError))
		}
	}
	merged.ErrorsTruncated = merged.ErrorsTruncated || left.ErrorsTruncated || right.ErrorsTruncated
	merged.Truncated = left.Truncated || right.Truncated || merged.ErrorsTruncated
	return merged
}

type artifactCollector struct {
	artifacts     []Artifact
	projectDir    string
	scope         []string
	coverageRoots []string
	metadata      ScanMetadata
}

func newArtifactCollector(projectDir string) *artifactCollector {
	return &artifactCollector{
		artifacts:     make([]Artifact, 0, maxArtifactsReported),
		projectDir:    projectDir,
		coverageRoots: make([]string, 0, maxScanCoverageRoots),
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
		c.metadata.Truncated = true
		return
	}
	scanError := sanitizeScanErrorText(c.projectDir, err.Error())
	message := relativeScanPath(c.projectDir, path) + ": " + scanError
	if c.metadata.ErrorBytes+int64(len(message)) > maxScanErrorBytes {
		c.metadata.ErrorsTruncated = true
		c.metadata.Truncated = true
		return
	}
	c.metadata.Errors = append(c.metadata.Errors, message)
	c.metadata.ErrorBytes += int64(len(message))
}

// addCoverage records only roots that can contain one of the supplied hints.
// The root is recorded after a complete walk, so hint suppression never relies
// on a path merely looking like a conventional Gradle output path.
func (c *artifactCollector) addCoverage(rootDir string, hints []string) {
	if len(hints) == 0 {
		return
	}
	if len(c.coverageRoots) >= maxScanCoverageRoots {
		c.metadata.Truncated = true
		return
	}
	root, ok := absoluteCleanPath(rootDir)
	if !ok {
		return
	}
	for _, hint := range hints {
		path, ok := resolveArtifactHintPath(c.projectDir, hint)
		if ok && pathWithin(root, path) {
			c.coverageRoots = append(c.coverageRoots, root)
			return
		}
	}
}

func (c *artifactCollector) covered(path string) bool {
	absolute, ok := absoluteCleanPath(path)
	if !ok {
		return false
	}
	for _, root := range c.coverageRoots {
		if pathWithin(root, absolute) {
			return true
		}
	}
	return false
}

func absoluteCleanPath(path string) (string, bool) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	return filepath.Clean(absolute), true
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
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

type SnapshotEntryMetadata struct {
	Discovered int
	Retained   int
	Truncated  bool
}

type SnapshotMetadata struct {
	ArtifactEntries SnapshotEntryMetadata
	ClassEntries    SnapshotEntryMetadata
	CodegenEntries  SnapshotEntryMetadata
}

func (m SnapshotMetadata) Truncated() bool {
	return m.ArtifactEntries.Truncated || m.ClassEntries.Truncated || m.CodegenEntries.Truncated
}

type Snapshot struct {
	Captured        bool                     `json:"-"`
	ArtifactEntries map[string]SnapshotEntry `json:"-"`
	ClassEntries    map[string]SnapshotEntry `json:"-"`
	CodegenEntries  map[string]SnapshotEntry `json:"-"`
	Metadata        SnapshotMetadata         `json:"-"`
}

type SnapshotEntry struct {
	ModTimeUnixNano int64  `json:"-"`
	SizeBytes       int64  `json:"-"`
	Kind            string `json:"-"`
}

type snapshotCandidate struct {
	path  string
	entry SnapshotEntry
}

// snapshotCandidateHeap is a max-heap: its root is the least valuable retained entry.
type snapshotCandidateHeap struct {
	values []snapshotCandidate
	less   func(snapshotCandidate, snapshotCandidate) bool
}

func (h snapshotCandidateHeap) Len() int           { return len(h.values) }
func (h snapshotCandidateHeap) Less(i, j int) bool { return h.less(h.values[j], h.values[i]) }
func (h snapshotCandidateHeap) Swap(i, j int)      { h.values[i], h.values[j] = h.values[j], h.values[i] }
func (h *snapshotCandidateHeap) Push(value any) {
	h.values = append(h.values, value.(snapshotCandidate))
}
func (h *snapshotCandidateHeap) Pop() any {
	last := len(h.values) - 1
	value := h.values[last]
	h.values = h.values[:last]
	return value
}

type snapshotEntryRetainer struct {
	entries  map[string]SnapshotEntry
	metadata *SnapshotEntryMetadata
	max      int
	heap     snapshotCandidateHeap
}

func newSnapshotEntryRetainer(entries map[string]SnapshotEntry, metadata *SnapshotEntryMetadata, max int, less func(snapshotCandidate, snapshotCandidate) bool) *snapshotEntryRetainer {
	return &snapshotEntryRetainer{
		entries: entries, metadata: metadata, max: max,
		heap: snapshotCandidateHeap{values: make([]snapshotCandidate, 0, max), less: less},
	}
}

func (r *snapshotEntryRetainer) retain(path string, entry SnapshotEntry) {
	if _, exists := r.entries[path]; exists {
		return
	}
	r.metadata.Discovered++
	candidate := snapshotCandidate{path: path, entry: entry}
	if r.heap.Len() < r.max {
		r.entries[path] = entry
		heap.Push(&r.heap, candidate)
		r.metadata.Retained++
		return
	}
	r.metadata.Truncated = true
	if !r.heap.less(candidate, r.heap.values[0]) {
		return
	}
	worst := heap.Pop(&r.heap).(snapshotCandidate)
	delete(r.entries, worst.path)
	r.entries[path] = entry
	heap.Push(&r.heap, candidate)
}

func artifactSnapshotCandidateLess(left, right snapshotCandidate) bool {
	return artifactLess(Artifact{Kind: left.entry.Kind, Path: left.path, SizeBytes: left.entry.SizeBytes}, Artifact{Kind: right.entry.Kind, Path: right.path, SizeBytes: right.entry.SizeBytes})
}

func lexicographicSnapshotCandidateLess(left, right snapshotCandidate) bool {
	return left.path < right.path
}

type artifactRoot struct {
	parts []string
}

var (
	rootedArtifactHintPattern   = regexp.MustCompile(`(?i)(?:file://)?(?:(?:[A-Za-z]:[\\/])|/|\./|\.\./)[^"'<>]*?(?:\.apk|\.aab|\.aar|\.jar|\.war|\.ear|\.zip|\.tgz|\.tar\.gz|\.tar|\.framework|\.xcframework|\.klib|\.kexe)(?:[\\/][^\s"'<>:,;]+)*`)
	quotedArtifactHintPattern   = regexp.MustCompile(`(?i)["']([^"']+\.(?:apk|aab|aar|jar|war|ear|zip|tgz|tar\.gz|tar|framework|xcframework|klib|kexe)(?:[\\/][^"']*)?)["']`)
	relativeArtifactHintPattern = regexp.MustCompile(`(?i)(?:\.[\\/]|[A-Za-z0-9_.-]+[\\/])[A-Za-z0-9_ ./\\-]*?\.(?:apk|aab|aar|jar|war|ear|zip|tgz|tar\.gz|tar|framework|xcframework|klib|kexe)(?:[\\/][A-Za-z0-9_ ./\\-]*)?`)
	artifactExtensionPattern    = regexp.MustCompile(`(?i)\.(?:apk|aab|aar|jar|war|ear|zip|tgz|tar\.gz|tar|framework|xcframework|klib|kexe)\b`)
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
	artifactEntries := newSnapshotEntryRetainer(snapshot.ArtifactEntries, &snapshot.Metadata.ArtifactEntries, maxSnapshotArtifactEntries, artifactSnapshotCandidateLess)
	classEntries := newSnapshotEntryRetainer(snapshot.ClassEntries, &snapshot.Metadata.ClassEntries, maxSnapshotClassEntries, lexicographicSnapshotCandidateLess)
	codegenEntries := newSnapshotEntryRetainer(snapshot.CodegenEntries, &snapshot.Metadata.CodegenEntries, maxSnapshotCodegenEntries, lexicographicSnapshotCandidateLess)

	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if buildDir, ok, skip := buildDirPath(path, entry); ok {
			captureBuildDir(buildDir, projectDir, artifactEntries, classEntries, codegenEntries)
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
	hints, hintsTruncated := boundScanHints(hints)
	threshold := startedAt.Add(-artifactTimeSkew)
	collector := newArtifactCollector(projectDir)
	collector.metadata.HintsTruncated = hintsTruncated
	collector.metadata.SnapshotTruncated = snapshot.Metadata.Truncated()
	collector.metadata.Truncated = hintsTruncated || collector.metadata.SnapshotTruncated
	classCount := 0
	codegenCount := 0

	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			collector.addError(path, walkErr)
			return nil
		}
		if buildDir, ok, skip := buildDirPath(path, entry); ok {
			scanBuildDir(buildDir, projectDir, threshold, snapshot, collector, hints, &classCount, &codegenCount)
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
	hints, hintsTruncated := boundScanHints(hints)
	collector := newScopedArtifactCollector(projectDir, projectPrefixes)
	collector.metadata.HintsTruncated = hintsTruncated
	collector.metadata.Truncated = hintsTruncated

	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			collector.addError(path, walkErr)
			return nil
		}
		if buildDir, ok, skip := buildDirPath(path, entry); ok {
			scanAvailableBuildDir(buildDir, projectDir, collector, hints)
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

func boundScanHints(hints []string) ([]string, bool) {
	if len(hints) == 0 {
		return nil, false
	}

	bounded := make([]string, 0, minInt(len(hints), maxScanHints))
	seen := make(map[string]struct{}, minInt(len(hints), maxScanHints))
	retainedBytes := 0
	truncated := false
	for _, raw := range hints {
		hint := normalizeArtifactHint(raw)
		if hint == "" {
			continue
		}
		if len(hint) > maxHintLength {
			truncated = true
			continue
		}
		if _, ok := seen[hint]; ok {
			continue
		}
		if len(bounded) >= maxScanHints || retainedBytes+len(hint) > maxScanHintBytes {
			truncated = true
			continue
		}
		hint = strings.Clone(hint)
		seen[hint] = struct{}{}
		bounded = append(bounded, hint)
		retainedBytes += len(hint)
	}
	return bounded, truncated
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// HintScanMetadata describes a streamed line scan. A zero limit means the
// scanner visits every match without building a match slice; non-zero limits
// bound callback count or candidate bytes and report when more input remains.
type HintScanMetadata struct {
	Parsed      int
	ParsedBytes int
	Truncated   bool
}

type hintMatcher struct {
	pattern *regexp.Regexp
	offset  int
	start   int
	end     int
	done    bool
}

func (m *hintMatcher) advance(text string) {
	if m.done {
		return
	}
	match := m.pattern.FindStringIndex(text[m.offset:])
	if match == nil {
		m.done = true
		return
	}
	m.start = m.offset + match[0]
	m.end = m.offset + match[1]
	m.offset = m.end
}

// ScanHints streams raw candidate matches from one log line. It keeps
// only three regexp positions live, so a large line cannot allocate an
// unbounded FindAll result. Limits are applied before the callback; pass zero
// for both limits when the caller needs complete, allocation-bounded parsing.
func ScanHints(text string, maxCount, maxBytes int, visit func(string)) HintScanMetadata {
	return scanHintCandidates(text, maxCount, maxBytes, visit, true)
}

func scanHintCandidates(text string, maxCount, maxBytes int, visit func(string), includeQuoted bool) HintScanMetadata {
	matchers := []hintMatcher{
		{pattern: rootedArtifactHintPattern},
		{pattern: relativeArtifactHintPattern},
	}
	if includeQuoted {
		matchers = append(matchers, hintMatcher{pattern: quotedArtifactHintPattern})
	}
	for i := range matchers {
		matchers[i].advance(text)
	}

	metadata := HintScanMetadata{}
	lastEnd := -1
	for {
		best := -1
		for i := range matchers {
			if matchers[i].done {
				continue
			}
			if best < 0 || matchers[i].start < matchers[best].start ||
				(matchers[i].start == matchers[best].start && matchers[i].end > matchers[best].end) {
				best = i
			}
		}
		if best < 0 {
			break
		}

		matcher := &matchers[best]
		start, end := matcher.start, matcher.end
		matcher.advance(text)
		if start < lastEnd {
			continue
		}

		if includeQuoted && best == len(matchers)-1 {
			quoted := scanQuotedHintCandidates(text, start, end, maxCount, maxBytes, metadata, visit)
			metadata.Parsed += quoted.Parsed
			metadata.ParsedBytes += quoted.ParsedBytes
			if quoted.Truncated {
				metadata.Truncated = true
				break
			}
			// A quoted match can contain plain matches. They were intentionally
			// consumed above; do not let the broad quote suppress their count.
			lastEnd = end
			continue
		}

		if !acceptHintCandidate(text[start:end], maxCount, maxBytes, &metadata, visit) {
			break
		}
		lastEnd = end
	}
	return metadata
}

func scanQuotedHintCandidates(text string, start, end, maxCount, maxBytes int, current HintScanMetadata, visit func(string)) HintScanMetadata {
	if end-start < 2 {
		return HintScanMetadata{}
	}
	if maxCount > 0 && current.Parsed >= maxCount {
		return HintScanMetadata{Truncated: true}
	}
	if maxBytes > 0 && current.ParsedBytes >= maxBytes {
		return HintScanMetadata{Truncated: true}
	}

	content := text[start+1 : end-1]
	if !hasMultipleArtifactExtensions(content) {
		var metadata HintScanMetadata
		acceptHintCandidate(content, remainingHintCount(maxCount, current.Parsed), remainingHintBytes(maxBytes, current.ParsedBytes), &metadata, visit)
		return metadata
	}

	return scanQuotedMultipleArtifactPaths(content, remainingHintCount(maxCount, current.Parsed), remainingHintBytes(maxBytes, current.ParsedBytes), visit)
}

func scanQuotedMultipleArtifactPaths(text string, maxCount, maxBytes int, visit func(string)) HintScanMetadata {
	metadata := HintScanMetadata{}
	offset := 0
	previousEnd := 0
	first := true
	for {
		match := artifactExtensionPattern.FindStringIndex(text[offset:])
		if match == nil {
			break
		}
		start := offset + match[0]
		end := offset + match[1]
		if !first && !quotedPathBoundary(text[previousEnd:start]) {
			previousEnd = end
			offset = end
			continue
		}
		candidateStart := previousEnd
		if !first {
			candidateStart = quotedPathStart(text, previousEnd, start)
		}
		candidate := strings.TrimSpace(text[candidateStart:end])
		candidate = strings.Trim(candidate, ",;:")
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "and ")
		candidate = strings.TrimSpace(candidate)
		if !acceptHintCandidate(candidate, maxCount, maxBytes, &metadata, visit) {
			break
		}
		previousEnd = end
		offset = end
		first = false
	}
	return metadata
}

func hasMultipleArtifactExtensions(text string) bool {
	first := artifactExtensionPattern.FindStringIndex(text)
	if first == nil {
		return false
	}
	previousEnd := first[1]
	for {
		next := artifactExtensionPattern.FindStringIndex(text[previousEnd:])
		if next == nil {
			return false
		}
		start := previousEnd + next[0]
		if quotedPathBoundary(text[previousEnd:start]) {
			return true
		}
		previousEnd += next[1]
	}
}

func quotedPathBoundary(segment string) bool {
	if segment == "" {
		return false
	}
	if segment[0] == '/' || segment[0] == '\\' {
		return false
	}
	return true
}

func quotedPathStart(text string, start, end int) int {
	if start >= end {
		return start
	}
	if text[start] == '/' || text[start] == '\\' {
		for i := end - 1; i >= start; i-- {
			if strings.ContainsRune(" \t,;:", rune(text[i])) {
				return i + 1
			}
		}
		return start
	}
	for start < end && strings.ContainsRune(" \t,;:", rune(text[start])) {
		start++
	}
	if strings.HasPrefix(text[start:end], "and ") {
		start += len("and ")
	}
	return start
}

func remainingHintCount(maxCount, parsed int) int {
	if maxCount == 0 {
		return 0
	}
	if parsed >= maxCount {
		return 1
	}
	return maxCount - parsed
}

func remainingHintBytes(maxBytes, parsedBytes int) int {
	if maxBytes == 0 {
		return 0
	}
	if parsedBytes >= maxBytes {
		return 1
	}
	return maxBytes - parsedBytes
}

func acceptHintCandidate(raw string, maxCount, maxBytes int, metadata *HintScanMetadata, visit func(string)) bool {
	if maxCount > 0 && metadata.Parsed >= maxCount {
		metadata.Truncated = true
		return false
	}
	candidateBytes := len(raw)
	if maxBytes > 0 && metadata.ParsedBytes+candidateBytes > maxBytes {
		metadata.Truncated = true
		return false
	}
	if visit != nil {
		visit(raw)
	}
	metadata.Parsed++
	metadata.ParsedBytes += candidateBytes
	return true
}

func ExtractHints(text string) []string {
	seen := make(map[string]struct{}, maxHintsPerLine)
	hints := make([]string, 0, maxHintsPerLine)
	ScanHints(text, maxHintsPerLine, maxHintBytesPerLine, func(raw string) {
		addBoundedHint(&hints, seen, raw)
	})
	if len(hints) == 0 {
		return nil
	}
	return hints
}

func addBoundedHint(hints *[]string, seen map[string]struct{}, raw string) {
	if len(raw) > maxHintLength+64 {
		return
	}
	hint := normalizeArtifactHint(raw)
	if hint == "" || len(hint) > maxHintLength {
		return
	}
	if _, ok := seen[hint]; ok {
		return
	}
	hint = strings.Clone(hint)
	seen[hint] = struct{}{}
	*hints = append(*hints, hint)
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

func captureBuildDir(buildDir, projectDir string, artifacts, classes, codegen *snapshotEntryRetainer) {
	for _, root := range artifactRoots {
		captureArtifactRoot(filepath.Join(append([]string{buildDir}, root.parts...)...), projectDir, artifacts)
	}

	captureMatchingFiles(filepath.Join(buildDir, "classes"), projectDir, classes, func(path string) bool {
		return strings.HasSuffix(strings.ToLower(path), ".class")
	})
	captureMatchingFiles(filepath.Join(buildDir, "tmp", "kotlin-classes"), projectDir, classes, func(path string) bool {
		return strings.HasSuffix(strings.ToLower(path), ".class")
	})
	captureMatchingFiles(filepath.Join(buildDir, "generated"), projectDir, codegen, func(path string) bool {
		return true
	})
}

func scanBuildDir(buildDir, projectDir string, threshold time.Time, snapshot Snapshot, collector *artifactCollector, hints []string, classCount, codegenCount *int) {
	for _, root := range artifactRoots {
		scanArtifactRoot(filepath.Join(append([]string{buildDir}, root.parts...)...), projectDir, threshold, snapshot, collector, hints)
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

func scanAvailableBuildDir(buildDir, projectDir string, collector *artifactCollector, hints []string) {
	for _, root := range artifactRoots {
		scanAvailableArtifactRoot(filepath.Join(append([]string{buildDir}, root.parts...)...), projectDir, collector, hints)
	}
}

func captureArtifactRoot(rootDir, projectDir string, retainer *snapshotEntryRetainer) {
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
				retainer.retain(artifact.Path, state)
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
		retainer.retain(artifact.Path, state)
		return nil
	})
}

func scanArtifactRoot(rootDir, projectDir string, threshold time.Time, snapshot Snapshot, collector *artifactCollector, hints []string) {
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

	complete := true
	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			complete = false
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
	if complete {
		collector.addCoverage(rootDir, hints)
	}
}

func captureMatchingFiles(rootDir, projectDir string, retainer *snapshotEntryRetainer, match func(path string) bool) {
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
		retainer.retain(relativePath, state)
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
	if collector.covered(resolvedPath) {
		// A complete scan already covered this path. Do not turn a repeated log
		// hint into a second discovery after the original candidate was evicted.
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
	if collector.covered(resolvedPath) {
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
		Kind:            kind,
	}
	if entry.IsDir() {
		state = directorySnapshot(path, info.ModTime())
		state.Kind = kind
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

func scanAvailableArtifactRoot(rootDir, projectDir string, collector *artifactCollector, hints []string) {
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

	complete := true
	_ = filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			complete = false
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
	if complete {
		collector.addCoverage(rootDir, hints)
	}
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

// NormalizeHint applies the same path cleanup used by ExtractHints without
// copying the source string. Callers retaining the result should clone it.
func NormalizeHint(hint string) string {
	return normalizeArtifactHint(hint)
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
