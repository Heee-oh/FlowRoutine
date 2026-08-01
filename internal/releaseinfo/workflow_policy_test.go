package releaseinfo

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var (
	actionReferencePattern = regexp.MustCompile(`(?m)^\s*uses:\s*([^\s#]+)`)
	immutableActionPattern = regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
)

func TestWorkflowActionsUseImmutableCommits(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve workflow policy test path")
	}
	workflowFiles, err := filepath.Glob(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("find workflow files: %v", err)
	}
	if len(workflowFiles) == 0 {
		t.Fatal("no GitHub Actions workflows found")
	}
	for _, workflowFile := range workflowFiles {
		contents, err := os.ReadFile(workflowFile)
		if err != nil {
			t.Fatalf("read %s: %v", workflowFile, err)
		}
		for _, match := range actionReferencePattern.FindAllStringSubmatch(string(contents), -1) {
			reference := strings.Trim(match[1], `"'`)
			if strings.HasPrefix(reference, "./") {
				continue
			}
			if !immutableActionPattern.MatchString(reference) {
				t.Errorf("%s uses mutable action reference %q; pin it to a full commit SHA", filepath.Base(workflowFile), reference)
			}
		}
	}
}
