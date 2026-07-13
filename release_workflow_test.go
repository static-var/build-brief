package buildbrief_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseWorkflowDryRunGatesReleaseMutations(t *testing.T) {
	workflow := parseReleaseWorkflow(t, ".github/workflows/release.yml")

	publish := workflow.input(t, "publish")
	if publish.defaultValue != "false" || publish.inputType != "boolean" {
		t.Fatalf("publish input must be a false-defaulted boolean, got default=%q type=%q", publish.defaultValue, publish.inputType)
	}
	if publish.description != "Publish release mutations. When false, validation may upload artifacts and use action caches, but makes no repository commit/tag/push, GitHub Release mutation, or Homebrew mutation." {
		t.Fatalf("publish input description must define the dry-run invariant precisely, got %q", publish.description)
	}

	publishHomebrew := workflow.input(t, "publish_homebrew")
	if publishHomebrew.defaultValue != "false" || publishHomebrew.inputType != "boolean" || publishHomebrew.description != "Publish the generated formula to Homebrew only when publish is also true" {
		t.Fatalf("publish_homebrew input must be a false-defaulted boolean documented as dependent on publish, got %+v", publishHomebrew)
	}

	for name, guard := range map[string]string{
		"Commit version bump and changelog": "inputs.publish && steps.prepare.outputs.needs_commit == 'true'",
		"Create release tag":                "inputs.publish && steps.prepare.outputs.tag_exists != 'true'",
		"Push release commit":               "inputs.publish && steps.prepare.outputs.needs_commit == 'true'",
		"Push release tag":                  "inputs.publish && steps.prepare.outputs.tag_exists != 'true'",
		"Publish GitHub release":            "inputs.publish",
		"Publish Homebrew tap":              "inputs.publish && inputs.publish_homebrew",
	} {
		step := workflow.step(t, name)
		if step.ifCondition != guard {
			t.Errorf("release mutation step %q must be gated by %q, got %q", name, guard, step.ifCondition)
		}
	}
}

func TestReleaseWorkflowPreflightsHomebrewTokenBeforeReleaseMutations(t *testing.T) {
	workflow := parseReleaseWorkflow(t, ".github/workflows/release.yml")
	preflight := workflow.step(t, "Preflight Homebrew token")
	if preflight.ifCondition != "inputs.publish && inputs.publish_homebrew" {
		t.Fatalf("Homebrew token preflight must be guarded by publish inputs, got %q", preflight.ifCondition)
	}
	if !strings.Contains(preflight.run, "HOMEBREW_TAP_TOKEN is not set") || !strings.Contains(preflight.run, "${HOMEBREW_TAP_TOKEN:-}") {
		t.Fatalf("Homebrew token preflight must fail closed when the token is absent, got:\n%s", preflight.run)
	}

	content, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	preflightAt := strings.Index(string(content), "- name: Preflight Homebrew token")
	if preflightAt < 0 {
		t.Fatal("release workflow is missing Homebrew token preflight")
	}
	for _, mutation := range []string{
		"- name: Commit version bump and changelog",
		"- name: Create release tag",
		"- name: Push release commit",
		"- name: Push release tag",
		"- name: Publish GitHub release",
		"- name: Publish Homebrew tap",
	} {
		if mutationAt := strings.Index(string(content), mutation); mutationAt < preflightAt {
			t.Fatalf("Homebrew token preflight must precede %q", mutation)
		}
	}
}

func TestReleaseWorkflowGeneratesNotesForExistingAndDryRunTags(t *testing.T) {
	workflow := parseReleaseWorkflow(t, ".github/workflows/release.yml")
	notes := workflow.step(t, "Generate GitHub changelog notes")
	if !strings.Contains(notes.run, "releases/generate-notes") {
		t.Fatalf("unexpected notes generation command:\n%s", notes.run)
	}
	if notes.env["RELEASE_TAG"] != "${{ steps.prepare.outputs.tag }}" || !strings.Contains(notes.run, `-f tag_name="$RELEASE_TAG"`) {
		t.Fatalf("notes generation must pass the release tag through env, got env=%q run:\n%s", notes.env["RELEASE_TAG"], notes.run)
	}
	if notes.env["RELEASE_SHA"] != "${{ github.sha }}" || !strings.Contains(notes.run, `-f target_commitish="$RELEASE_SHA"`) {
		t.Fatalf("notes generation must pass the dispatched SHA through env, got env=%q run:\n%s", notes.env["RELEASE_SHA"], notes.run)
	}
}

func TestReleaseWorkflowDoesNotInterpolateExpressionsIntoShell(t *testing.T) {
	workflow := parseReleaseWorkflow(t, ".github/workflows/release.yml")

	for name, step := range workflow.steps {
		for _, expression := range []string{"${{ inputs.", "${{ vars."} {
			if strings.Contains(step.run, expression) {
				t.Errorf("step %q interpolates %q into shell source; use env and quoted shell variables instead:\n%s", name, expression, step.run)
			}
		}
	}

	for stepName, expectedEnv := range map[string]map[string]string{
		"Sync branch state": {
			"RELEASE_REF_NAME": "${{ github.ref_name }}",
		},
		"Prepare release metadata": {
			"RELEASE_BUMP":    "${{ inputs.bump }}",
			"RELEASE_VERSION": "${{ inputs.version }}",
		},
		"Build release artifacts": {
			"RELEASE_VERSION": "${{ steps.prepare.outputs.version }}",
		},
		"Generate Homebrew formula": {
			"RELEASE_REPOSITORY": "${{ github.repository }}",
			"RELEASE_VERSION":    "${{ steps.prepare.outputs.version }}",
		},
		"Validate Homebrew formula": {
			"RELEASE_VERSION": "${{ steps.prepare.outputs.version }}",
		},
		"Commit version bump and changelog": {
			"RELEASE_TAG": "${{ steps.prepare.outputs.tag }}",
		},
		"Create release tag": {
			"RELEASE_TAG": "${{ steps.prepare.outputs.tag }}",
		},
		"Push release commit": {
			"RELEASE_REF_NAME": "${{ github.ref_name }}",
		},
		"Push release tag": {
			"RELEASE_TAG": "${{ steps.prepare.outputs.tag }}",
		},
		"Generate GitHub changelog notes": {
			"RELEASE_NOTES_FILE": "${{ steps.prepare.outputs.notes_file }}",
			"RELEASE_REPOSITORY": "${{ github.repository }}",
			"RELEASE_SHA":        "${{ github.sha }}",
			"RELEASE_TAG":        "${{ steps.prepare.outputs.tag }}",
		},
		"Publish GitHub release": {
			"RELEASE_NOTES_FILE": "${{ steps.github-notes.outputs.notes_file }}",
			"RELEASE_REPOSITORY": "${{ github.repository }}",
			"RELEASE_TAG":        "${{ steps.prepare.outputs.tag }}",
		},
		"Publish Homebrew tap": {
			"HOMEBREW_TAP_REPOSITORY": "${{ vars.HOMEBREW_TAP_REPOSITORY }}",
			"HOMEBREW_TAP_BRANCH":     "${{ vars.HOMEBREW_TAP_BRANCH }}",
			"RELEASE_VERSION":         "${{ steps.prepare.outputs.version }}",
		},
	} {
		step := workflow.step(t, stepName)
		for name, value := range expectedEnv {
			if step.env[name] != value {
				t.Errorf("step %q must wire %s through env, got %q", stepName, name, step.env[name])
			}
		}
	}

	prepare := workflow.step(t, "Prepare release metadata")
	if !strings.Contains(prepare.run, `--bump "$RELEASE_BUMP"`) || !strings.Contains(prepare.run, `--version "$RELEASE_VERSION"`) {
		t.Fatalf("prepare step must pass release inputs as quoted shell variables, got:\n%s", prepare.run)
	}
}

func TestReleaseWorkflowQuotesMaliciousVersionBeforePrepareReleaseValidation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "shell-injection-marker")
	maliciousVersion := `1.2.3"; touch "` + marker + `"; #`
	outputDir := t.TempDir()

	command := exec.Command("bash", "-c", `scripts/prepare-release.sh --bump "$RELEASE_BUMP" --version "$RELEASE_VERSION" --output-dir "$RELEASE_OUTPUT_DIR"`)
	command.Env = append(os.Environ(), "RELEASE_BUMP=patch", "RELEASE_VERSION="+maliciousVersion, "RELEASE_OUTPUT_DIR="+outputDir)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("malicious version must fail validation, output:\n%s", output)
	}
	if strings.Contains(string(output), "command not found") {
		t.Fatalf("malicious version was parsed as shell syntax, output:\n%s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("malicious version escaped shell quoting; marker state error: %v", err)
	}
}

func TestReleaseWorkflowKeepsValidationEvidenceAndCacheEnabledInDryRun(t *testing.T) {
	workflow := parseReleaseWorkflow(t, ".github/workflows/release.yml")

	artifactUpload := workflow.step(t, "Upload workflow artifacts")
	if artifactUpload.uses != "actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2" {
		t.Fatalf("unexpected artifact upload action: %q", artifactUpload.uses)
	}
	if artifactUpload.ifCondition != "" {
		t.Fatalf("artifact upload is intentional dry-run evidence and must not be publish-gated, got %q", artifactUpload.ifCondition)
	}

	setupGo := workflow.step(t, "Set up Go")
	if setupGo.with["cache"] != "true" {
		t.Fatalf("Go action cache is intentional dry-run optimization and must remain enabled, got %q", setupGo.with["cache"])
	}
}

type releaseWorkflow struct {
	inputs map[string]releaseWorkflowInput
	steps  map[string]releaseWorkflowStep
}

type releaseWorkflowInput struct {
	description  string
	defaultValue string
	inputType    string
}

type releaseWorkflowStep struct {
	ifCondition string
	uses        string
	run         string
	env         map[string]string
	with        map[string]string
}

func parseReleaseWorkflow(t *testing.T, path string) releaseWorkflow {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}

	workflow := releaseWorkflow{inputs: map[string]releaseWorkflowInput{}, steps: map[string]releaseWorkflowStep{}}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		if hasExactIndent(line, 6) && strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "      - ") {
			name := strings.TrimSuffix(strings.TrimSpace(line), ":")
			input := releaseWorkflowInput{}
			for index++; index < len(lines) && !hasExactIndent(lines[index], 6); index++ {
				key, value, ok := yamlScalarLine(lines[index], 8)
				if !ok {
					continue
				}
				switch key {
				case "description":
					input.description = value
				case "default":
					input.defaultValue = value
				case "type":
					input.inputType = value
				}
			}
			workflow.inputs[name] = input
			index--
			continue
		}

		if !hasExactIndent(line, 6) || !strings.HasPrefix(line, "      - name: ") {
			continue
		}
		name := strings.TrimPrefix(line, "      - name: ")
		step := releaseWorkflowStep{env: map[string]string{}, with: map[string]string{}}
		inEnv := false
		inRun := false
		inWith := false
		for index++; index < len(lines) && !(hasExactIndent(lines[index], 6) && strings.HasPrefix(lines[index], "      - name: ")); index++ {
			key, value, ok := yamlScalarLine(lines[index], 8)
			if ok {
				inEnv = key == "env" && value == ""
				inRun = key == "run" && value == "|"
				inWith = key == "with" && value == ""
				switch key {
				case "if":
					step.ifCondition = strings.TrimPrefix(strings.TrimSuffix(value, "}"), "${{ ")
				case "run":
					if value != "|" {
						step.run = value + "\n"
					}
				case "uses":
					step.uses = value
				}
				continue
			}
			envKey, envValue, envOK := yamlScalarLine(lines[index], 10)
			if inEnv && envOK {
				step.env[envKey] = envValue
			}
			withKey, withValue, withOK := yamlScalarLine(lines[index], 10)
			if inWith && withOK {
				step.with[withKey] = withValue
			}
			if inRun && (hasExactIndent(lines[index], 10) || hasExactIndent(lines[index], 12)) {
				step.run += lines[index] + "\n"
			}
		}
		workflow.steps[name] = step
		index--
	}
	return workflow
}

func hasExactIndent(line string, spaces int) bool {
	return len(line) > spaces && line[:spaces] == strings.Repeat(" ", spaces) && line[spaces] != ' '
}

func yamlScalarLine(line string, indent int) (key, value string, ok bool) {
	if !hasExactIndent(line, indent) {
		return "", "", false
	}
	key, value, ok = strings.Cut(strings.TrimSpace(line), ":")
	if !ok {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	return key, value, true
}

func (workflow releaseWorkflow) input(t *testing.T, name string) releaseWorkflowInput {
	t.Helper()
	input, ok := workflow.inputs[name]
	if !ok {
		t.Fatalf("release workflow is missing input %q", name)
	}
	return input
}

func (workflow releaseWorkflow) step(t *testing.T, name string) releaseWorkflowStep {
	t.Helper()
	step, ok := workflow.steps[name]
	if !ok {
		t.Fatalf("release workflow is missing named step %q", name)
	}
	return step
}
