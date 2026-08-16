package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Closure-taking generic array methods (`xs.reduce(f)` / `xs.sort_by(cmp)` /
// `xs.filter(pred)` and `any` / `all` / `find`) lower on the self-host IR path
// (array-method monomorphisation, slice 3). The slice-1/2 path folded only
// closure-free array methods (concat); these carry a closure ("fn") parameter,
// which it excluded. The receiver alone fixes the instantiation `T`, and the
// closure rides through as a fn value — register_array_method_generics now folds
// `(xs: T[]) reduce(f)` into a free generic `__arrm_reduce[T](xs, f)` whose body
// delegates to the free `reduce`, which already lowers closures on the IR path.
// (Methods carrying their OWN type params — map[U] / flat_map[U] / fold[A] /
// zip[U] — stay deferred: the result type variable needs closure-return-type
// inference the receiver does not supply.)
//
// Each case uses a single element type, which is what these cases are about;
// the same verb at several element types in one program is covered by
// TestSelfHostArrayMethodMultiElemIR. Each case is checked against the
// interpreter oracle.
var arrayClosureMethodIRCases = []struct {
	name string
	src  string
}{
	// reduce: seedless left fold returning Option[T].
	{"reduce", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; match (xs.reduce(function (a: i32, b: i32): i32 { return a + b; })) { Some(v) => { return v; }, None => { return 0; } } }`},
	// sort_by: comparator-driven sort, read back as a scalar.
	{"sort_by", `import "std/array";
function main(): i32 { var xs: i32[] = [3, 1, 2]; var s: i32[] = xs.sort_by(function (a: i32, b: i32): i32 { return a - b; }); return s[0] * 100 + s[1] * 10 + s[2]; }`},
	// filter: predicate keep, reduced to a scalar.
	{"filter", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5, 6]; var e: i32[] = xs.filter(function (n: i32): boolean { return n % 2 == 0; }); return e.len() * 100 + e[0] * 10 + e[2]; }`},
	// find: first matching element as Option[T].
	{"find", `import "std/array";
function main(): i32 { var xs: i32[] = [5, 8, 3, 9]; match (xs.find(function (n: i32): boolean { return n > 7; })) { Some(v) => { return v; }, None => { return 0; } } }`},
	// find_last: LAST matching element (right-to-left mirror of find). In
	// [5,8,3,9,2] the elements > 4 are 5,8,9 — the last is 9.
	{"find_last", `import "std/array";
function main(): i32 { var xs: i32[] = [5, 8, 3, 9, 2]; match (xs.find_last(function (n: i32): boolean { return n > 4; })) { Some(v) => { return v; }, None => { return 0; } } }`},
	// rposition: index of the LAST matching element. In [1,2,3,2,1] the 2s
	// are at indices 1 and 3 — rposition returns 3.
	{"rposition", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 2, 3, 2, 1]; match (xs.rposition(function (n: i32): boolean { return n == 2; })) { Some(i) => { return i; }, None => { return 9; } } }`},
}

func TestSelfHostArrayClosureMethodIR(t *testing.T) {
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

	for _, tc := range arrayClosureMethodIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "acm_"+tc.name+".fern")
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
			if !strings.Contains(asm, "__arrm_"+tc.name+"__") {
				t.Errorf("%s: no monomorphised __arrm_%s__ clone in asm (method did not ride the IR array-method path)", tc.name, tc.name)
			}
			bin := buildBin(t, gcc, dir, "acm_"+tc.name+"_bin", asm)
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
