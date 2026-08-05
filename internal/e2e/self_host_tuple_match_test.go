package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleMatchCases pin tuple-pattern match arms
// `match (p) { (0, y) => …, (x, y) => … }` on the self-host compiler.
// The parser desugars the whole match at parse time (build_tuple_match)
// into a destructure + flag-guarded if chain, so these ride constructs
// every self-host backend already lowers. Covers literal dispatch,
// binder extraction, a guard (which must see the arm's binders), a
// trailing `_` arm, a string element, and the expression form (IIFE).
var selfHostTupleMatchCases = []struct {
	name string
	src  string
	exit int
}{
	{"literal-dispatch", "function classify(p: (i32, i32)): i32 { match (p) { (0, 0) => { return 1; }, (0, y) => { return y; }, (x, 0) => { return x * 10; }, (x, y) when x > y => { return x - y; }, (x, y) => { return x + y; } } return -1; } function main(): i32 { return classify((0, 0)) + classify((0, 7)) + classify((3, 0)) + classify((9, 4)) + classify((2, 5)) - 8; }", 42},
	{"wildcard-arm", "function pick(p: (i32, i32)): i32 { match (p) { (1, y) => { return y; }, _ => { return 40; } } return -1; } function main(): i32 { return pick((1, 2)) + pick((9, 9)); }", 42},
	{"string-element", "function tag(p: (string, i32)): i32 { match (p) { (\"a\", n) => { return n; }, (_, n) => { return n * 10; } } return -1; } function main(): i32 { return tag((\"a\", 2)) + tag((\"z\", 4)); }", 42},
	{"expr-form", "function main(): i32 { var v = match ((1, 2)) { (1, b) => b * 20, (a, _) => a }; return v + 2; }", 42},
	// The expression form with a CAPTURING scrutinee (#6010). `expr-form`
	// above matches a tuple LITERAL, which the lift hoists to a real
	// `__lam_N` function — so its arms' `return`s were contained and the bug
	// never showed. Match a LOCAL instead and the lift has to leave the IIFE
	// inline, where the desugar's arm `return`s escaped and returned from
	// main: this program answered 1, never reaching the final return. The
	// value now goes through a local (IfChain.value_local), so the chain
	// assigns and the IIFE has exactly one terminal return.
	{"expr-form-capturing", "function main(): i32 { var p = (1, 2); var v = match (p) { (x, _) => x }; return v + 41; }", 42},
	// Two of them in one function: under the bug the FIRST one returned and
	// everything after it was dead code.
	{"expr-form-two", "function main(): i32 { var p = (1, 2); var q = (7, 7); var a = match (p) { (1, b) => b * 10, (x, _) => x }; var b = match (q) { (0, y) => y, (x, y) when x == y => x + y, (x, y) => x - y }; return a + b + 8; }", 42},
	// A block-bodied arm over a captured local — the leading statements stay
	// in place and only the value-producing tail becomes the store.
	{"expr-form-block-body", "function main(): i32 { var p = (3, 4); var v = match (p) { (x, y) => { var s = x + y; s * 6 } }; return v; }", 42},
	// The STRUCT-pattern desugar (build_struct_match) splices arm bodies into
	// the same done-flag chain, so it had the identical escape.
	{"expr-form-struct-pattern", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 40, y: 2 }; var v = match (p) { P { x, y } => x + y }; return v; }", 42},
}

// TestSelfHostTupleMatchX86_64 compiles each case through the
// self-hosted x86-64 driver (asm_run) and asserts the exit code.
func TestSelfHostTupleMatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range selfHostTupleMatchCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
