package gradle

import (
	"reflect"
	"testing"
)

func TestAnalyzeArgsCollectsExcludedTasks(t *testing.T) {
	for _, args := range [][]string{
		{"check", "-x", ":app:test"},
		{"check", "--exclude-task", ":app:test"},
		{"check", "--exclude-task=:app:test"},
		{"check", "-x:app:test"},
	} {
		shape := AnalyzeArgs(args)
		if !reflect.DeepEqual(shape.ExcludedTasks, []string{":app:test"}) || !reflect.DeepEqual(shape.TaskSelectors, []string{"check"}) {
			t.Errorf("AnalyzeArgs(%q) = %+v", args, shape)
		}
	}
}
