package sourcelint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var emitCallRe = regexp.MustCompile(`\b(?:x86_64|arm64codegen|arm64|wasm)\.Emit\(\s*(\w+)\s*,\s*(\w+)\s*\)`)

const monomorphLookback = 4000

func TestEveryBackendEmitIsMonomorphised(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	var offenders []string
	for _, dir := range []string{"internal", "cmd"} {
		werr := filepath.Walk(filepath.Join(root, dir), func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			s := string(src)
			for _, m := range emitCallRe.FindAllStringSubmatchIndex(s, -1) {
				prog := s[m[2]:m[3]]
				start := m[0] - monomorphLookback
				if start < 0 {
					start = 0
				}
				run := regexp.MustCompile(`monomorph\.Run\(\s*` + regexp.QuoteMeta(prog) + `\b`)
				if run.MatchString(s[start:m[0]]) {
					continue
				}
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, fmt.Sprintf("%s: %s has no preceding monomorph.Run(%s, …)",
					rel, s[m[0]:m[1]], prog))
			}
			return nil
		})
		if werr != nil {
			t.Fatalf("walk %s: %v", dir, werr)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("backend Emit without monomorphisation (%d site(s)) — see #7773:\n  %s\n\n"+
			"Add `monomorph.Run(prog, info)` after checker.Check and before Emit. Feeding Emit an\n"+
			"un-monomorphised program does not fail: it emits an erased generic whose T[] element\n"+
			"read truncates a pointer, and the resulting binary segfaults somewhere unrelated.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
