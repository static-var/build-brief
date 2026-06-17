package doctor

import (
	"fmt"
	"io"
	"strings"
)

func RenderHuman(report Report) string {
	var b strings.Builder
	_ = WriteHuman(&b, report)
	return b.String()
}

func WriteHuman(w io.Writer, report Report) error {
	if _, err := fmt.Fprintln(w, "Build Brief Doctor"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	groups := orderedGroups(report)
	for gi, group := range groups {
		if gi > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w, group); err != nil {
			return err
		}
		for _, result := range report.Results {
			if result.Group != group {
				continue
			}
			if _, err := fmt.Fprintf(w, "  %s %s: %s\n", result.Status, result.Name, result.Summary); err != nil {
				return err
			}
			for _, detail := range result.Detail {
				if _, err := fmt.Fprintf(w, "    - %s\n", detail); err != nil {
					return err
				}
			}
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	result := "healthy"
	if report.HasFailures() {
		result = "problems found"
	} else if SummaryStatus(report) == StatusWarn {
		result = "healthy with warnings"
	}
	_, err := fmt.Fprintf(w, "Result: %s\n", result)
	return err
}

func orderedGroups(report Report) []string {
	seen := map[string]bool{}
	groups := []string{}
	for _, result := range report.Results {
		if !seen[result.Group] {
			seen[result.Group] = true
			groups = append(groups, result.Group)
		}
	}
	return groups
}
