package sourcelint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestImageOnlyScopeReachesSelfHostLane(t *testing.T) {
	for _, tc := range []struct{ file, job, value string }{
		{ciFile, "suite", "${{ needs.changes.outputs.image-only == 'true' }}"},
		{ciSuiteFile, "test-e2e-selfhost", "${{ inputs.image-only }}"},
	} {
		found := false
		for _, job := range workflowJobs(t, tc.file) {
			if job.id == tc.job {
				found = true
				if job.with["image-only"] != tc.value {
					t.Errorf("%s: %s must pass image-only as %s", tc.file, tc.job, tc.value)
				}
			}
		}
		if !found {
			t.Errorf("%s: job %s is missing", tc.file, tc.job)
		}
	}
	for _, file := range []string{ciSuiteFile, "test-e2e-selfhost.yml"} {
		on, _ := onBlock(workflowSource(t, file))
		if !strings.Contains(on, "      image-only:\n") || !strings.Contains(on, "        default: false\n") {
			t.Errorf("%s must default image-only to false", file)
		}
	}
	for _, job := range workflowJobs(t, "test-e2e-selfhost.yml") {
		if job.id == "image-consumers" {
			if job.keys["if"] != "inputs.image-only" || job.keys["needs"] != "" {
				t.Error("image consumers must run independently when image-only is true")
			}
		} else if !strings.Contains(job.keys["if"], "!inputs.image-only") {
			t.Errorf("compiler job %s must exclude image-only changes", job.id)
		}
	}
}

func TestImageConsumerSelectionCoversImageReaders(t *testing.T) {
	job := jobBlocks(workflowSource(t, "test-e2e-selfhost.yml"))["image-consumers"]
	selectors := regexp.MustCompile(`-run '([^']+)'`).FindAllStringSubmatch(job, -1)
	if len(selectors) == 0 || strings.Count(job, "-count=1") != len(selectors) {
		t.Error("image consumer checks must disable the Go test cache for external Fern inputs")
	}
	var patterns []*regexp.Regexp
	for _, selector := range selectors {
		patterns = append(patterns, regexp.MustCompile(selector[1]))
	}
	selected := func(name string) bool {
		for _, pattern := range patterns {
			if pattern.MatchString(name) {
				return true
			}
		}
		return false
	}
	for _, name := range []string{
		"TestGrammarDerivesSelfHostParsedSources",
		"TestSelfHostShowcaseHTTP",
		"TestSelfHostGatherConcurrency",
	} {
		if !selected(name) {
			t.Errorf("image consumers must select %s", name)
		}
	}
	if !strings.Contains(job, "FERN_SELFHOST_FIXTURES: \"1\"") {
		t.Error("the grammar check requires FERN_SELFHOST_FIXTURES")
	}
	root := mustRepoRoot(t)
	readers := 0
	for _, dir := range []string{"e2e", "e2ecompiler"} {
		files, err := filepath.Glob(filepath.Join(root, "internal/testing", dir, "*_test.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			source, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(source), "fern-image/") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), file, source, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					literal, ok := n.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						return true
					}
					value, _ := strconv.Unquote(literal.Value)
					if strings.Contains(value, "fern-image/") {
						readers++
						if !selected(fn.Name.Name) {
							t.Errorf("%s: %s reads fern-image but the image consumers do not select it", file, fn.Name.Name)
						}
					}
					return true
				})
			}
		}
	}
	if readers == 0 {
		t.Fatal("no direct image reader found in the compiler tests")
	}
}
