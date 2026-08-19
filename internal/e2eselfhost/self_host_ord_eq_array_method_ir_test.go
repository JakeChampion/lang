package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// std/array's element-polymorphic method forms of the cmp.Ord / cmp.Eq verbs
// (`xs.is_sorted()`, `xs.equal(ys)`, `xs.starts_with(p)`, `xs.ends_with(s)`,
// `xs.index_of_last(t)`) on the self-host IR path. These are bounded-receiver
// generic array methods — `(xs: T[]) is_sorted[T: cmp.Ord]()` etc. — so they
// exercise the fold that routes a bounded receiver through IR. Each is read-only
// over its inputs, so it is correct on the self-host IR path as well as native;
// the assertions below would catch both a wrong result and an accidental bail
// (the `-decide` check).
//
// One element type per program here; the folded-method monomorphiser emits one
// clone per (generic name, element type), and the multi-element-type case is
// covered by TestSelfHostArrayMethodMultiElemIR.
var ordEqArrayMethodIRCases = []struct {
	name string
	src  string
}{
	// is_sorted: [1,2,3] sorted, [3,1,2] not — both checks pass → 3.
	{"is_sorted", `import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = [3, 1, 2];
    var r: i32 = 0;
    if (a.is_sorted()) { r = r + 1; }
    if (!b.is_sorted()) { r = r + 2; }
    return r;
}`},
	// equal: structural element-wise equality over string[].
	{"equal", `import "std/array";
function main(): i32 {
    var a: string[] = ["x", "y"];
    var b: string[] = ["x", "y"];
    var c: string[] = ["x", "z"];
    var r: i32 = 0;
    if (a.equal(b)) { r = r + 1; }
    if (!a.equal(c)) { r = r + 2; }
    return r;
}`},
	// starts_with over i32[].
	{"starts_with", `import "std/array";
function main(): i32 {
    var a: i32[] = [1, 2, 3, 4];
    var p: i32[] = [1, 2];
    var q: i32[] = [2, 3];
    var r: i32 = 0;
    if (a.starts_with(p)) { r = r + 1; }
    if (!a.starts_with(q)) { r = r + 2; }
    return r;
}`},
	// ends_with over string[].
	{"ends_with", `import "std/array";
function main(): i32 {
    var a: string[] = ["a", "b", "c"];
    var s: string[] = ["b", "c"];
    if (a.ends_with(s)) { return 1; }
    return 0;
}`},
	// index_of_last: last index of 5 in [5,3,5,1] is 2.
	{"index_of_last", `import "std/array";
function main(): i32 {
    var a: i32[] = [5, 3, 5, 1];
    match (a.index_of_last(5)) { Some(i) => { return i; }, None => { return 99; } }
}`},
}

// TestSelfHostOrdEqArrayMethodIR — the read-only Ord/Eq array method forms route
// the self-host IR path and run correctly, cross-checked against the interpreter.
func TestSelfHostOrdEqArrayMethodIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "oealr")
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

	for _, tc := range ordEqArrayMethodIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "ordeq_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "ordeq_"+tc.name+"_bin", asm)
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
