package civalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIWorkflowClassifiesRequiredPathClassesConservatively(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	contents, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	for _, fragment := range []string{
		"*.md|docs/*|openspec/*",
		"web/*|api/*|cmd/*|internal/*|go.mod|go.sum|tools/*|Makefile|database/*|migrations/*|queries/*|sqlc.yaml|deploy/acceptance/*|.github/workflows/*",
		"cmd/*|internal/*|go.mod|go.sum|database/*|migrations/*|queries/*|sqlc.yaml",
		"cmd/*|internal/*|go.mod|go.sum|migrations/*|queries/*|sqlc.yaml|deploy/acceptance/*|.github/workflows/*",
		"deploy/acceptance/*|.github/workflows/*",
		"ci_required:",
		"if: ${{ always() }}",
		"unexpected_skip=0 coverage_gap=0",
	} {
		if !strings.Contains(workflow, fragment) {
			t.Errorf("CI classifier/aggregate contract lacks %q", fragment)
		}
	}
}

func TestCIWorkflowKeepsCapacityAndCompatibilityOutOfMainCorrectnessJobs(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	main := string(ci)
	if strings.Contains(main, "TestAccountInventoryReadonlyQueryCapacityOneTenFifty") ||
		strings.Contains(main, "TestAccountInventoryLifecycleCapacityOneTenFifty") ||
		strings.Contains(main, "official_snapshot:") {
		t.Fatal("main correctness workflow still owns capacity or upstream compatibility")
	}
	for _, path := range []string{".github/workflows/capacity.yml", ".github/workflows/compatibility.yml"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("replacement workflow missing: %s: %v", path, err)
		}
	}
}
