package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// One generic array-receiver method used at SEVERAL element types in the same
// program, on the self-host IR path.
//
// `register_array_method_generics` folds `(xs: T[]) m(args)` into a free
// generic `__arrm_m[T]` and the free-generic worklist clones one
// `__arrm_m__<elem>` per element type. A guard in mono_expr used to suppress
// every instantiation after the first, so a program calling `xs.map(f)` on
// both `i32[]` and `string[]` left the second call in method form. That guard
// dated from when an unlowered method still had the AST emitter to fall back
// to; with the AST emitters retired (#5972) it turned into a hard
// "module is not IR-eligible" refusal, so mixing element types made a program
// uncompilable by the self-host compiler while native compiled it fine.
//
// Each case asserts three things together: the module decides `ir`, a distinct
// `__arrm_<name>__<elem>` clone is emitted for EVERY element type used (one
// clone reaching the asm while another call silently kept the method form is
// the exact shape of the old bug), and the binary agrees with the interpreter.
var arrayMethodMultiElemIRCases = []struct {
	name string
	src  string
	syms []string
}{
	// map at two element types: [1,2,3] doubled indexes 6 at [2], plus a
	// 2-element string[] -> 6 + 2 = 8.
	{"map-i32-string", `import "std/array";
function dbl(x: i32): i32 { return x * 2; }
function id_s(s: string): string { return s; }
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var ss: string[] = ["a", "b"];
    var a: i32[] = xs.map(dbl);
    var b: string[] = ss.map(id_s);
    return a[2] + b.len();
}`, []string{"__arrm_map__i32", "__arrm_map__string"}},

	// Three element types through one method, including a wide (i64) element.
	{"map-i32-string-i64", `import "std/array";
function dbl(x: i32): i32 { return x * 2; }
function id_s(s: string): string { return s; }
function id_l(x: i64): i64 { return x; }
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var ss: string[] = ["a", "b"];
    var ls: i64[] = [7i64];
    return xs.map(dbl)[2] + ss.map(id_s).len() + (ls.map(id_l)[0] as i32);
}`, []string{"__arrm_map__i32", "__arrm_map__string", "__arrm_map__i64"}},

	// concat — a 2-param `T[]`-only helper. This is the shape the old guard
	// named as crashing at two element types; it lowers and runs correctly now.
	{"concat-i32-string", `import "std/array";
function main(): i32 {
    var xs: i32[] = [1, 2];
    var ys: i32[] = [3];
    var ss: string[] = ["a"];
    var ts: string[] = ["b", "c"];
    var a: i32[] = xs.concat(ys);
    var b: string[] = ss.concat(ts);
    return a.len() * 10 + b.len();
}`, []string{"__arrm_concat__i32", "__arrm_concat__string"}},

	// An Ord-BOUNDED receiver method at two element types: the bound resolves
	// `i32.cmp` in one clone and `string.cmp` in the other.
	{"is_sorted-bounded", `import "std/array";
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var ss: string[] = ["b", "a"];
    var a: boolean = xs.is_sorted();
    var b: boolean = ss.is_sorted();
    if (a && !b) { return 7; }
    return 9;
}`, []string{"__arrm_is_sorted__i32", "__arrm_is_sorted__string"}},

	// An Eq-bounded receiver-only method (no `T`-typed argument to infer from,
	// so the element type comes from the receiver alone).
	{"dedup-bounded", `import "std/array";
function main(): i32 {
    var xs: i32[] = [1, 1, 2];
    var ss: string[] = ["a", "a", "b", "c"];
    return xs.dedup().len() * 10 + ss.dedup().len();
}`, []string{"__arrm_dedup__i32", "__arrm_dedup__string"}},

	// `rotate_left(n: i32)` has no `T`-typed argument, so it was throttled like
	// the rest — but its mis-dispatch target `i32.rotate_left` HAPPENS to exist
	// (std/i32's BITWISE rotate, i32.fern), so the string arm silently called
	// the bitwise routine with an array pointer instead of failing. Strict-IR
	// was satisfied because a symbol of that name resolved. Only the oracle
	// comparison catches that shape, which is why this case pins the answer as
	// well as both clones.
	{"rotate_left-silent-misdispatch", `import "std/array";
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var ss: string[] = ["a", "b", "c"];
    var a: i32[] = xs.rotate_left(1);
    var b: string[] = ss.rotate_left(1);
    return a[0] * 10 + b[0].len() + b.len();
}`, []string{"__arrm_rotate_left__i32", "__arrm_rotate_left__string"}},

	// A chained pipeline per element type — the surface #2663 asks for. Only
	// `map` clones per element here: `filter`'s receiver is a call expression
	// rather than a bare local, so it rides the erased uniform-width path on
	// one shared clone. That is correct for a callback-driven verb (the body
	// never names the element type) and the oracle check is what pins it.
	{"chained-pipeline", `import "std/array";
function dbl(x: i32): i32 { return x * 2; }
function big(x: i32): boolean { return x > 2; }
function id_s(s: string): string { return s; }
function nonempty(s: string): boolean { return s.len() > 0; }
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var ss: string[] = ["a", "", "b"];
    var a: i32[] = xs.map(dbl).filter(big);
    var b: string[] = ss.map(id_s).filter(nonempty);
    return a.len() * 10 + b.len();
}`, []string{"__arrm_map__i32", "__arrm_map__string", "__arrm_filter__"}},

	// The same verb at four element widths in one program, narrow and wide:
	// a shared erased `filter` clone has to stay correct across a 4-byte i32,
	// an 8-byte i64 / f64 and a 16-byte string.
	{"filter-narrow-and-wide", `import "std/array";
function bigi(x: i32): boolean { return x > 1; }
function bigl(x: i64): boolean { return x > 1i64; }
function bigf(x: f64): boolean { return x > 1.0; }
function nonempty(s: string): boolean { return s.len() > 0; }
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    var ls: i64[] = [1i64, 5i64, 9i64];
    var fs: f64[] = [0.5, 2.5];
    var ss: string[] = ["a", "", "b"];
    var a = xs.filter(bigi);
    var b = ls.filter(bigl);
    var c = fs.filter(bigf);
    var d = ss.filter(nonempty);
    return a.len() * 1000 + b.len() * 100 + c.len() * 10 + d.len();
}`, []string{"__arrm_filter__"}},
}

func TestSelfHostArrayMethodMultiElemIR(t *testing.T) {
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

	for _, tc := range arrayMethodMultiElemIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "arrmulti_"+tc.name+".fern")
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
			for _, sym := range tc.syms {
				if !strings.Contains(asm, sym) {
					t.Errorf("%s: no %s clone in asm — that element type's call did not ride the IR array-method path", tc.name, sym)
				}
			}
			bin := buildBin(t, gcc, dir, "arrmulti_"+tc.name+"_bin", asm)
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
