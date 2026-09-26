package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `xs.map(f).reduce(g)` on the self-host IR path (#9743).
//
// `register_array_method_generics` folds an array-receiver method into a free
// generic `__arrm_m[T]` whose `type_params` carries only the BOUNDED receiver
// var — an unbounded param of the method's own, `map[U]`, is erased from it
// while still appearing in the return spelling `U[]`. `mono_infer`'s
// array-method arm substituted the receiver var alone, so a chained call
// received the type VARIABLE `U[]` as though it were a type: a `reduce` hung
// off it instantiated as `__arrm_reduce__U` with `ret_type = Option[U]`, the
// match binding found no class for the payload tag `U` and took a default i32
// slot, and the i64 return path then refused it — "did not lower: `return` of
// ident `v`". With no AST emitter behind it, the whole module was refused.
//
// The arm now finishes the job the way `call_ret_type` already does two
// branches away: a type var still residual after the receiver substitution is
// bound by unifying the whole parameter list against the arguments
// (`resolve_residual_ret` / `full_ret_binds`), which recovers `U` from the
// lambda's declared return.
//
// `reduce` is what broke because it is the one `std/array` method whose result
// type comes solely from the bounded receiver var AND whose result binds a
// payload. The neighbours below masked the same bad inference rather than
// escaping it — `map(f).fold(z, g)` takes its width from the `init` argument,
// `filter(p).reduce(h)` has nothing residual to lose — so they are controls:
// they passed before and must still pass, or the repair moved the problem
// instead of fixing it.
//
// Three binding shapes, because all three failed. Repairing this at the
// irlower annotation override would have left two of them broken: what was
// wrong is the type `mono_infer` returns, not how an annotation reaches a slot.
var arrmResidualRetIRCases = []struct {
	name string
	src  string
}{
	{"annotated-var", `import "std/array";
function f(xs: i64[]): i64 {
    var out: Option[i64] = xs.map((x: i64): i64 => x + (1 as i64)).reduce((a: i64, b: i64): i64 => a + b);
    match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return f([1 as i64, 2 as i64]) as i32; }`},
	{"unannotated-var", `import "std/array";
function f(xs: i64[]): i64 {
    var out = xs.map((x: i64): i64 => x + (1 as i64)).reduce((a: i64, b: i64): i64 => a + b);
    match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return f([1 as i64, 2 as i64]) as i32; }`},
	{"inline-match", `import "std/array";
function f(xs: i64[]): i64 {
    match (xs.map((x: i64): i64 => x + (1 as i64)).reduce((a: i64, b: i64): i64 => a + b)) {
        Some(v) => { return v; },
        None => { return 0 as i64; }
    }
}
function main(): i32 { return f([1 as i64, 2 as i64]) as i32; }`},
	// A payload that is not a scalar: the match binding reaches a string slot
	// only if `U` resolved, so this failed the same way while exercising a
	// different slot class.
	{"string-payload", `import "std/array";
import "std/string";
function f(xs: i64[]): i32 {
    var out: Option[string] = xs.map((x: i64): string => "a").reduce((a: string, b: string): string => a + b);
    match (out) { Some(v) => { return v.len(); }, None => { return 0; } }
}
function main(): i32 { return f([1 as i64, 2 as i64, 3 as i64]); }`},
	// Three stages, so the residual has to survive being carried through a
	// second chained method rather than only out of the first.
	{"filter-map-reduce", `import "std/array";
function f(xs: i64[]): i64 {
    var out: Option[i64] = xs.filter((x: i64): boolean => x > (0 as i64))
                             .map((x: i64): i64 => x + (1 as i64))
                             .reduce((a: i64, b: i64): i64 => a + b);
    match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return f([1 as i64, 2 as i64, 3 as i64]) as i32; }`},
	{"control-reduce-alone", `import "std/array";
function f(xs: i64[]): i64 {
    var out: Option[i64] = xs.reduce((a: i64, b: i64): i64 => a + b);
    match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return f([1 as i64, 2 as i64]) as i32; }`},
	{"control-split-statements", `import "std/array";
function f(xs: i64[]): i64 {
    var ys: i64[] = xs.map((x: i64): i64 => x + (1 as i64));
    var out: Option[i64] = ys.reduce((a: i64, b: i64): i64 => a + b);
    match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return f([1 as i64, 2 as i64]) as i32; }`},
	{"control-map-fold", `import "std/array";
function f(xs: i64[]): i64 {
    return xs.map((x: i64): i64 => x + (1 as i64)).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return f([1 as i64, 2 as i64]) as i32; }`},
}

func TestSelfHostArrmResidualRetIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "arrmres")
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

	for _, tc := range arrmResidualRetIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "arrmres_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")

			// `-decide` is the assertion this test exists for: the bug was a
			// REFUSAL, so a regression shows up here as "refused" (or as an empty
			// emit below) rather than as a wrong answer.
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\" — the IR path declined the module again", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "arrmres_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (interpreter oracle) — it lowered but computed "+
					"the wrong answer, which is worse than the refusal this test was written against",
					tc.name, code, want)
			}
		})
	}
}
