package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Iterator-bounded generic reducers (core/iter) on the self-host IR path.
// These route a bounded generic `[I: Iterator[i32]]` / `[T, I: Iterator[T]]`
// reducer over `iter.of(xs)` — where `of[T](xs: T[]): ArrayIter[T]` and
// `impl[T] Iterator[T] for ArrayIter[T]`. The 2026-06-22 FEATURE-AUDIT recorded
// `sum` / `count` / `to_array` as ineligible (decide=ast)
// because the parser type-erased the unbounded `T`, leaving a dangling
// `ArrayIter[T]` that never lowered. That gap is now closed: targeted promotion
// of the unbounded `T` (only when it feeds a parametric type AND is bindable
// from a param) + a clone-time `Self`-instantiation resolution (return/param
// type normalisation + body struct-literal retarget) + symbol-safe clone names
// route the whole `of` -> `next` -> reducer chain through the self-host IR path.
// Each case is decide-checked "ir" and oracle-checked against the interpreter.
var iterBoundedReducerIRCases = []struct {
	name string
	src  string
}{
	// sum[I: Iterator[i32]] over ArrayIter[i32]: 1+2+3+4 = 10.
	{"sum", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; return iter.sum(iter.of(xs)); }`},
	// sum over an empty array: the first next() is None, total stays 0.
	{"sum-empty", `import "core/iter";
function main(): i32 { var xs: i32[] = []; return iter.sum(iter.of(xs)); }`},
	// product[I: Iterator[i32]]: 1*2*3*4 = 24.
	{"product", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; return iter.product(iter.of(xs)); }`},
	// count[T, I: Iterator[T]]: the unbounded T stays erased (only in the
	// trait bound), I monomorphises to ArrayIter[i32]; yields 5.
	{"count", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5]; return iter.count(iter.of(xs)); }`},
	// to_array[T, I: Iterator[T]]: T erased, returns T[]; len of the collected
	// array is 3.
	{"to_array-len", `import "core/iter";
function main(): i32 { var xs: i32[] = [9, 8, 7]; var ys: i32[] = iter.to_array(iter.of(xs)); return ys.len(); }`},
	// to_array element fidelity: ys[1] == 8.
	{"to_array-elem", `import "core/iter";
function main(): i32 { var xs: i32[] = [9, 8, 7]; var ys: i32[] = iter.to_array(iter.of(xs)); return ys[1]; }`},
	// sum over a Range iterator (the non-generic Iterator impl) still routes IR:
	// 1+2+3+4 = 10.
	{"range-sum", `import "core/iter";
function main(): i32 { return iter.sum(iter.range(1, 5)); }`},
	// std/num.sum_iter[T: Add, I: iter.Iterator[T]] — a TWO bounded-param reducer
	// where I instantiates to a module-mangled struct (`iter__ArrayIter[i32]`).
	// Exercises the ';'-joined multi-param instantiation key (split_inst_key):
	// a '__'-join would shatter the key on the embedded '__' of `iter__ArrayIter`
	// and bind I to the bogus "iter". 4+5+6 = 15.
	{"num-sum_iter", `import "core/iter";
import "std/num";
function main(): i32 { var xs: i32[] = [4, 5, 6]; return num.sum_iter(iter.of(xs), 0); }`},
	// num.sum_iter over empty: returns the zero unchanged (0).
	{"num-sum_iter-empty", `import "core/iter";
import "std/num";
function main(): i32 { var xs: i32[] = []; return num.sum_iter(iter.of(xs), 0); }`},
	// num.product_iter[T: Mul, I: iter.Iterator[T]]: 2*3*4 = 24.
	{"num-product_iter", `import "core/iter";
import "std/num";
function main(): i32 { var xs: i32[] = [2, 3, 4]; return num.product_iter(iter.of(xs), 1); }`},
}

func TestSelfHostIterBoundedReducersIR(t *testing.T) {
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

	for _, tc := range iterBoundedReducerIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "iterbnd_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "iterbnd_"+tc.name+"_bin", asm)
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
