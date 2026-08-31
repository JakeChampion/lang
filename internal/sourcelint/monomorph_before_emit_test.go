package sourcelint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A backend's `Emit(prog, info)` expects a program the pre-codegen pipeline has
// already finished with: constfold, check, AND monomorph. cmd/fern runs all
// three. The e2e harnesses open-code the same sequence at ~75 sites, and 39 of
// them omitted the monomorph step (#7773).
//
// The omission is invisible until a generic reaches one of those sites. An
// un-instantiated generic keeps its erased type parameter, so a `T[]` element
// read loads at i32 width against an 8-byte stride and the pointer arrives
// truncated to its low half. That is not a diagnostic — it is a segfault in a
// binary the test then measures, attributed to whatever the test was about.
//
// It has now cost two separate investigations. CompileAndRunX86_64 was fixed
// when a heap-layout shift perturbed it into view; emitDriverAsm and 37 other
// sites were fixed when the self-host adopted its second generic function and
// every driver binary the suite builds started segfaulting on the module-loading
// path. Both times the compiler was correct and only the harness was not.
//
// So this is a rule rather than a habit: every `Emit(prog, info)` must be
// preceded by a `monomorph.Run(prog, …)` on the SAME program variable, within
// the same function. Scanned over the Go sources that call a backend directly.
var emitCallRe = regexp.MustCompile(`\b(?:x86_64|arm64codegen|arm64|wasm)\.Emit\(\s*(\w+)\s*,\s*(\w+)\s*\)`)

// monomorphLookback is how far back to search for the pass. It is generous on
// purpose: a site that runs monomorph much earlier than its Emit is unusual but
// correct, and a false positive here blocks a legitimate change.
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
