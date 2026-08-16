package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Type-parameter generic array methods (`xs.map[U](f)` / `xs.flat_map[U](f)` /
// `xs.fold[A](init, f)` / `xs.zip[U](other)`) lower on the self-host IR path
// (array-method monomorphisation, slice 4). Slice 3 folded methods whose only
// type variable was the receiver's `T`; these also carry an UNBOUNDED extra
// type variable (`U` / `A`). The self-host erases unbounded type variables under
// its uniform 8-byte ABI — the result's element width is driven by the CALL
// SITE's annotation (`var ys: string[] = xs.map(f)`), not by cloning the body —
// so the receiver alone fixes the monomorphised `T` and the folded body delegates
// to the free `map` / `flat_map` / `fold` / `zip`, all of which already lower on
// IR for any `U`/`A` (incl. a width-changing one, e.g. i32 -> string). This
// flipped `array_hof` from AST to IR.
//
// Each case uses a single element type; the same verb at several element types
// in one program is covered by TestSelfHostArrayMethodMultiElemIR. Each case is
// checked against the interpreter oracle.
var arrayTyparamMethodIRCases = []struct {
	name string
	src  string
}{
	// map: i32 -> i32 (same width).
	{"map", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys: i32[] = xs.map(function (n: i32): i32 { return n * 10; }); return ys[2]; }`},
	// map: i32 -> string (the result element width changes — erasure + call-site
	// annotation must drive it).
	{"map-widen", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 22, 333]; var ys: string[] = xs.map(function (n: i32): string { return n.to_string(); }); return ys[2].len() * 10 + ys.len(); }`},
	// flat_map: T -> U[] then flatten.
	{"flat_map", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys: i32[] = xs.flat_map(function (x: i32): i32[] { return [x, x * 10]; }); return ys.len() * 100 + ys[1]; }`},
	// fold: string accumulator (A differs from T, and is pointer-width).
	{"fold-widen", `import "std/array";
function main(): i32 { var xs: i32[] = [1, 2, 3]; var s: string = xs.fold("", function (a: string, n: i32): string { return a + n.to_string(); }); return s.len(); }`},
	// zip: pairs into a (T, U)[] tuple array.
	{"zip", `import "std/array";
function main(): i32 { var a: i32[] = [1, 2, 3]; var b: i32[] = [9, 8]; var z: (i32, i32)[] = a.zip(b); return z.len() * 10 + z[0].0; }`},
}

func TestSelfHostArrayTyparamMethodIR(t *testing.T) {
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

	for _, tc := range arrayTyparamMethodIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "atm_"+tc.name+".fern")
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
			if !strings.Contains(asm, "__arrm_") {
				t.Errorf("%s: no monomorphised __arrm_ clone in asm (method did not ride the IR array-method path)", tc.name)
			}
			bin := buildBin(t, gcc, dir, "atm_"+tc.name+"_bin", asm)
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
