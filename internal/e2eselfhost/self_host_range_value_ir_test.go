package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Range EXPRESSIONS `a..b` / `a..=b` as first-class iterator VALUES on the
// self-host IR path (issue #2699's remaining part). `a..b` desugars to
// `iter.range(a, b)` and `a..=b` to `iter.range_incl(a, b)` — core/iter's Range,
// which implements Iterator — so a range flows through the whole combinator
// surface (sum / count / map / …) exactly like `iter.of(xs)`. The `for i in
// LOW..HIGH` loop keeps its separate optimized counted-loop desugar; these cases
// exercise the EXPRESSION form (range as a value passed to / bound by code).
// Each routes "ir" and is oracle-checked against the interpreter.
var rangeValueIRCases = []struct {
	name string
	src  string
}{
	// Half-open: sum(0..10) = 0+…+9 = 45.
	{"sum-half-open", `import "core/iter";
function main(): i32 { return iter.sum(0..10); }`},
	// Inclusive: sum(0..=10) = 0+…+10 = 55.
	{"sum-inclusive", `import "core/iter";
function main(): i32 { return iter.sum(0..=10); }`},
	// count over a non-zero-based range: count(2..7) = 5.
	{"count", `import "core/iter";
function main(): i32 { return iter.count(2..7); }`},
	// Precedence: `..` binds looser than `+`, so 0..n+1 is 0..(n+1) → sum 0..4 = 6.
	{"precedence", `import "core/iter";
function main(): i32 { var n: i32 = 3; return iter.sum(0..n+1); }`},
	// Range bound to a var, then consumed as a value.
	{"bound-to-var", `import "core/iter";
function main(): i32 { var r = 1..5; return iter.sum(r); }`},
	// product over an inclusive range: 1*2*3*4 = 24.
	{"product-inclusive", `import "core/iter";
function main(): i32 { return iter.product(1..=4); }`},
	// Empty range: sum(5..5) = 0.
	{"empty", `import "core/iter";
function main(): i32 { return iter.sum(5..5); }`},
}

func TestSelfHostRangeValueIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range rangeValueIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "rangeval_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "rangeval_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
