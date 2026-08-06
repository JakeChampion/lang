package wasmbin

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// The runtime-helper dependency closure.
//
// scanRuntimeHelpers is a flat pass: each `case` hand-lists the
// helpers its op needs, and until closeUnconditionalHelperCalls a
// callee's OWN callees had to be repeated at every one of those sites.
// Breaking that rule fails silently and remotely — a missing name
// reads 0 out of helperIdxs, the body emits `call 0`, and that
// resolves to whatever occupies funcidx 0. It has shipped twice:
// #4816 (`__fern_str_dec` without `__fern_box_free`, landing on a
// 5-param comparator) and then `__fern_temp_dir` / `__fern_read_dir`
// pulling in `__fern_str_copy` without its `__fern_alloc_rc1`.

// TestUnconditionalHelperCallsClose pins the mechanism: a set holding
// only a caller must come back holding everything it calls,
// transitively. The transitive leg is the one worth stating — str_copy
// reaches __fern_alloc only through __fern_alloc_rc1, so a
// single-step pass would leave it out.
func TestUnconditionalHelperCallsClose(t *testing.T) {
	for caller, callees := range unconditionalHelperCalls {
		t.Run(caller, func(t *testing.T) {
			var needs runtimeNeeds
			needs.add(caller)
			closeUnconditionalHelperCalls(&needs)
			for _, callee := range callees {
				if !needs.set[callee] {
					t.Errorf("%s did not pull in %s", caller, callee)
				}
			}
		})
	}
	var needs runtimeNeeds
	needs.add("__fern_str_copy")
	closeUnconditionalHelperCalls(&needs)
	if !needs.set["__fern_alloc"] {
		t.Error("__fern_str_copy did not transitively pull in __fern_alloc (via __fern_alloc_rc1)")
	}
}

// TestTempDirOnlyProgramIsValidWasm is the regression for the bug
// itself, at the level where it was observable.
//
// A program calling ONLY temp_dir is the smallest one whose helper set
// contains __fern_str_copy and nothing else that happens to drag
// __fern_alloc_rc1 in — which is why the bug survived part 1's tests:
// every one of them also read, wrote or listed, and each of those
// paths pulls rc1 in by another route. Without the closure this
// module fails validation with "type mismatch: expected i32, found
// i64", because str_copy's `call 0` lands on the get-random-u64 import
// temp_dir itself pulled in.
func TestTempDirOnlyProgramIsValidWasm(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	src := `function main(): i32 {
    match (temp_dir("solo")) { Ok(p) => { return 0; }, Err(e) => { return 1; } }
    return 1;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := Build(prog, info)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := filepath.Join(t.TempDir(), "solo.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("a temp_dir-only module must be valid wasm: %v\n%s", err, out)
	}
}
