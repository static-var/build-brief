package gradle

import "strings"

type InvocationShape struct {
	TaskSelectors       []string
	ExcludedTasks       []string
	IsPureInformational bool
}

var informationalTasks = map[string]struct{}{
	"tasks":                    {},
	"help":                     {},
	"projects":                 {},
	"dependencies":             {},
	"dependencyInsight":        {},
	"dependencyManagement":     {},
	"buildEnvironment":         {},
	"javaToolchains":           {},
	"outgoingVariants":         {},
	"resolvableConfigurations": {},
	"properties":               {},
	"artifactTransforms":       {},
	"kotlinDslAccessorsReport": {},
}

func AnalyzeArgs(args []string) InvocationShape {
	shape := InvocationShape{
		TaskSelectors: []string{},
		ExcludedTasks: []string{},
	}

	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}

		switch {
		case arg == "-x" || arg == "--exclude-task":
			if i+1 < len(args) {
				shape.ExcludedTasks = append(shape.ExcludedTasks, args[i+1])
				i++
			}
			continue
		case consumesNextArg(arg):
			if i+1 < len(args) {
				i++
			}
			continue
		case strings.HasPrefix(arg, "-"):
			continue
		case looksLikeGradleInvocation(arg):
			continue
		default:
			shape.TaskSelectors = append(shape.TaskSelectors, arg)
		}
	}

	shape.IsPureInformational = len(shape.TaskSelectors) > 0
	for _, selector := range shape.TaskSelectors {
		if !IsInformationalTaskSelector(selector) {
			shape.IsPureInformational = false
			break
		}
	}

	return shape
}

func IsInformationalTaskSelector(selector string) bool {
	_, ok := informationalTasks[taskSelectorName(selector)]
	return ok
}

func taskSelectorName(selector string) string {
	name := strings.TrimSpace(selector)
	if index := strings.LastIndex(name, ":"); index >= 0 {
		name = name[index+1:]
	}
	return name
}

func consumesNextArg(arg string) bool {
	switch arg {
	case "--console",
		"--warning-mode",
		"--gradle-user-home",
		"-g",
		"--project-dir",
		"-p",
		"--project-prop",
		"--system-prop",
		"-P",
		"-D",
		"--tests",
		"--task",
		"--dependency",
		"--configuration",
		"--init-script",
		"-I",
		"--settings-file",
		"-c",
		"--build-file",
		"-b",
		"--include-build",
		"--project-cache-dir",
		"--max-workers",
		"--configuration-cache-problems",
		"--priority",
		"--update-locks",
		"--write-verification-metadata",
		"--dependency-verification",
		"--refresh-keys",
		"--export-keys":
		return true
	default:
		return false
	}
}
