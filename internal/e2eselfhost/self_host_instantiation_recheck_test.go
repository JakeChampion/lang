package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// goPipelineDiags is what `fern -check` reports for src: the checker, then —
// when that is clean — monomorphisation, whose re-check of the instantiated
// copies is where a generic's substituted types are judged. checker.Check
// alone never sees them (#10018).
func goPipelineDiags(t *testing.T, dir, src string) []driverDiag {
	t.Helper()
	p := filepath.Join(dir, "gopipeline_input.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write pipeline input: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		return nil
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return checkErrDiags(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return checkErrDiags(err)
	}
	return checkErrDiags(monomorph.Run(prog, info))
}

// diagMultiset is diagLines with duplicates kept: an instantiation reported
// once per call site instead of once per distinct copy is a count bug the
// sorted-unique comparisons elsewhere cannot see.
func diagMultiset(ds []driverDiag) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.code+": "+d.msg)
	}
	sort.Strings(out)
	return out
}

// TestSelfHostInstantiationRecheckDifferentialX86_64 pins that the self-host
// checker judges a generic's instantiations the way native's monomorph
// re-check does — the same diagnostics, the same number of times.
func TestSelfHostInstantiationRecheckDifferentialX86_64(t *testing.T) {
	checkerBin, runner, dir := buildCheckerCodesBin(t)

	for _, tc := range instantiationRecheckRows {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			got := diagMultiset(driverDiags(runCheckerDriver(t, cmd, tc.name)))
			want := diagMultiset(goPipelineDiags(t, dir, tc.src))
			if !equalStrings(got, want) {
				t.Errorf("%s: the two checkers disagree about this program's instantiations.\nnative:    %s\nself-host: %s\nsrc:\n%s",
					tc.name, joinOrNone(want), joinOrNone(got), tc.src)
			}
			if len(want) != tc.native {
				t.Errorf("%s: native reports %d diagnostics, the row expects %d — the row is wrong, not the compilers\nsrc:\n%s",
					tc.name, len(want), tc.native, tc.src)
			}
		})
	}
}

var instantiationRecheckRows = []struct {
	name   string
	src    string
	native int // how many diagnostics native reports
}{
	// The #10018 program: no float key is written, `build` is instantiated at
	// f64. One E045 for the copy's return type, one for its local.
	{"float-key-through-a-generic", `import "core/map";
function build[T](k: T, v: i32): Map[T, i32] {
    var m: Map[T, i32] = map_new(2);
    return m.insert(k, v);
}
function main(): i32 { return build(2.5, 7).get_or(2.5, 0); }
`, 2},
	// One copy per distinct instantiation, however many calls ask for it —
	// including the one another generic's copy asks for.
	{"one-copy-per-instantiation", `import "core/map";
function build[T](k: T, v: i32): Map[T, i32] {
    var m: Map[T, i32] = map_new(2);
    return m.insert(k, v);
}
function wrap[U](k: U): i32 { return build(k, 1).get_or(k, 0); }
function main(): i32 { return build(2.5, 7).get_or(2.5, 0) + build(1.5, 7).get_or(1.5, 0) + wrap(true) + wrap(3.5); }
`, 2},
	// Two refused instantiations each get their own pair; the accepted ones
	// get nothing.
	{"each-refused-instantiation", `import "core/map";
function build[T](k: T, v: i32): Map[T, i32] {
    var m: Map[T, i32] = map_new(2);
    return m.insert(k, v);
}
function main(): i32 {
    var a: f32 = 1.5;
    return build(2.5, 7).get_or(2.5, 0) + build(a, 1).get_or(a, 0) + build(3, 1).get_or(3, 0) + build("s", 1).get_or("s", 0);
}
`, 4},
	// Reached only through another generic's copy, whose argument type is
	// the element of a substituted local.
	{"through-a-generic-copy", `import "core/map";
function keyed[K](k: K): Map[K, i32] { var m: Map[K, i32] = map_new(2); return m.insert(k, 1); }
function outer[T](x: T): i32 { var xs: T[] = [x]; return keyed(xs[0]).len(); }
function main(): i32 { return outer(1.5); }
`, 2},
	{"accepted-instantiations", `import "core/map";
function build[T](k: T, v: i32): Map[T, i32] {
    var m: Map[T, i32] = map_new(2);
    return m.insert(k, v);
}
function main(): i32 { return build(3, 1).get_or(3, 0) + build("s", 1).get_or("s", 0); }
`, 0},
	// A scalar carries no reference, so passing one to a consuming parameter
	// transfers nothing — a copy of `own x: T` at T = i32 is the common case,
	// and the written form is the same rule.
	{"scalar-own-through-a-generic", `function pass[T](own x: T): T { return x; }
function twice[T](own x: T): T { return pass(x); }
function main(): i32 { return twice(3); }
`, 0},
	{"scalar-own-parameter", `function g(own y: i32): i32 { return y; }
function f(own x: i32): i32 { return g(x); }
function main(): i32 { return f(1); }
`, 0},
	{"scalar-local-into-own", `function g(own y: i32): i32 { return y; }
function main(): i32 { var n: i32 = 4; var m = 5; return g(n) + g(m); }
`, 0},
	// A borrowed reference still is not an owned argument.
	{"borrowed-string-into-own", `function g(own y: string): string { return y; }
function f(x: string): string { return g(x); }
function main(): i32 { return f("a").len(); }
`, 1},
}
