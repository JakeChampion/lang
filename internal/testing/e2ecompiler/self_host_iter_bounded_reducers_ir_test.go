package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Iterator-bounded generic reducers (core/iter) on the self-host IR path.
// These route a bounded generic `[T, I: Iterator[T]]` reducer over
// `iter.of(xs)` — where `of[T](xs: T[]): ArrayIter[T]` and
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
	// sum over ArrayIter[i32]: 1+2+3+4 = 10.
	{"sum", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; return iter.sum(iter.of(xs)); }`},
	// sum over an empty array: the first next() is None, total stays 0.
	{"sum-empty", `import "core/iter";
function main(): i32 { let xs: i32[] = []; return iter.sum(iter.of(xs)); }`},
	// product: 1*2*3*4 = 24.
	{"product", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; return iter.product(iter.of(xs)); }`},
	// count[T, I: Iterator[T]]: the unbounded T stays erased (only in the
	// trait bound), I monomorphises to ArrayIter[i32]; yields 5.
	{"count", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4, 5]; return iter.count(iter.of(xs)); }`},
	// to_array[T, I: Iterator[T]]: T erased, returns T[]; len of the collected
	// array is 3.
	{"to_array-len", `import "core/iter";
function main(): i32 { let xs: i32[] = [9, 8, 7]; let ys: i32[] = iter.to_array(iter.of(xs)); return ys.len(); }`},
	// to_array element fidelity: ys[1] == 8.
	{"to_array-elem", `import "core/iter";
function main(): i32 { let xs: i32[] = [9, 8, 7]; let ys: i32[] = iter.to_array(iter.of(xs)); return ys[1]; }`},
	// sum over a Range iterator (the non-generic Iterator impl) still routes IR:
	// 1+2+3+4 = 10.
	{"range-sum", `import "core/iter";
function main(): i32 { return iter.sum(iter.range(1, 5)); }`},
	// sum[T: Add + Zero, I: Iterator[T]] — TWO bounded params, where I
	// instantiates to a module-mangled struct (`iter__ArrayIter[i64]`), so the
	// key is ';'-joined (split_inst_key), and T binds only from I's impl. The
	// result has no annotation to bind T from either. 3e9+3e9 = 6e9.
	{"sum-i64-unannotated", `import "core/iter";
function main(): i32 { let xs: i64[] = [3000000000, 3000000000]; let s = iter.sum(iter.of(xs)); if (s == 6000000000) { return 7; } return 1; }`},
	// T, bounded on its own trait, binds from a QUALIFIED bound
	// (`iter.Iterator[T]`) in the caller's module.
	{"user-qualified-bound", `import "core/iter";
import "std/num" as num;
function first_of[T: num.Add, I: iter.Iterator[T]](it: I): Option[T] {
  match (it.next()) { Some(t) => { return Some(t.0); }, None => { return None; } }
}
function main(): i32 { let xs: i64[] = [9, 2]; let f = first_of(iter.of(xs)); match (f) { Some(v) => { if (v == 9) { return 9; } return 1; }, None => { return 2; } } }`},
	// An integer literal argument settles at what the iterator's impl decides:
	// contains[T: Eq, I: Iterator[T]] over i64 with target `2` is the i64
	// instance, not an i32 one.
	{"contains-literal-i64", `import "core/iter";
function main(): i32 { let xs: i64[] = [3, 9, 2]; if (iter.contains(iter.of(xs), 2) && iter.count_value(iter.of(xs), 9) == 1) { return 5; } return 1; }`},
	// max[T: Ord, I: Iterator[T]] over f64.
	{"max-f64", `import "core/iter";
function main(): i32 { let xs: f64[] = [1.5, 4.5, 2.0]; match (iter.max(iter.of(xs))) { Some(m) => { if (m == 4.5) { return 4; } return 1; }, None => { return 2; } } }`},
}

func TestSelfHostIterBoundedReducersIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
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
