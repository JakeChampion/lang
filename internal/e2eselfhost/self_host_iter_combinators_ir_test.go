package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The full core/iter combinator surface on the self-host IR path, driven over a
// real `iter.of(xs)` (ArrayIter[i32]) — the predicate/closure-taking, value-
// returning, and array-collecting combinators all in one gate. After the
// Iterator-bounded reducer + multi-param-key landings, the remaining frontier was
// a closure-conversion gap: a fn-value ARGUMENT to a call in MATCH-SCRUTINEE
// position (`match (iter.find(iter.of(xs), named_fn)) { … }`) was never env-boxed
// (the lift's statement walker handled if/while/for conditions but not match
// scrutinees), so a NAMED-function predicate to `find` reached the callee as a
// raw fn-pointer where a closure box was expected and segfaulted (a lambda
// predicate, already a box, worked). With the match-scrutinee/arm walk added,
// every combinator below routes "ir" and matches the interpreter — including
// `find` with a named-function predicate.
var iterCombinatorIRCases = []struct {
	name string
	src  string
}{
	// find with a NAMED-function predicate in a match scrutinee — the case the
	// lift's match-scrutinee walk covers; without it this segfaults.
	{"find-named", `import "core/iter";
function gt1(x: i32): boolean { return x > 1; }
function main(): i32 { var xs: i32[] = [1, 2, 3]; match (iter.find(iter.of(xs), gt1)) { Some(v) => { return v; }, None => { return 0; } } }`},
	// find with a named predicate, no match (returns None → 7).
	{"find-named-none", `import "core/iter";
function big(x: i32): boolean { return x > 100; }
function main(): i32 { var xs: i32[] = [1, 2, 3]; match (iter.find(iter.of(xs), big)) { Some(v) => { return v; }, None => { return 7; } } }`},
	// find with a lambda predicate (closure box — worked before; guards the path).
	{"find-lambda", `import "core/iter";
function main(): i32 { match (iter.find(iter.of([1, 2, 3]), function (x: i32): boolean { return x >= 2; })) { Some(v) => { return v; }, None => { return 0; } } }`},
	// any / all over named predicates.
	{"any", `import "core/iter";
function gt2(x: i32): boolean { return x > 2; }
function main(): i32 { var xs: i32[] = [1, 2, 3]; if (iter.any(iter.of(xs), gt2)) { return 1; } return 0; }`},
	{"all", `import "core/iter";
function gt0(x: i32): boolean { return x > 0; }
function main(): i32 { var xs: i32[] = [1, 2, 3]; if (iter.all(iter.of(xs), gt0)) { return 1; } return 0; }`},
	// position_by / count_by (named predicate, i32 result).
	{"position_by", `import "core/iter";
function gt1(x: i32): boolean { return x > 1; }
function main(): i32 { var xs: i32[] = [1, 2, 3]; return iter.position_by(iter.of(xs), gt1); }`},
	{"count_by", `import "core/iter";
function gt1(x: i32): boolean { return x > 1; }
function main(): i32 { var xs: i32[] = [1, 2, 3]; return iter.count_by(iter.of(xs), gt1); }`},
	// flat_map (named fn returning an array).
	{"flat_map", `import "core/iter";
function dup(x: i32): i32[] { return [x, x]; }
function main(): i32 { var xs: i32[] = [1, 2]; var ys: i32[] = iter.flat_map(iter.of(xs), dup); return ys.len(); }`},
	// enumerate / zip → tuple-array collectors.
	{"enumerate", `import "core/iter";
function main(): i32 { var xs: i32[] = [5, 6, 7]; var ys: (i32, i32)[] = iter.enumerate(iter.of(xs)); return ys.len(); }`},
	{"zip", `import "core/iter";
function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = [3, 4]; var ys: (i32, i32)[] = iter.zip(iter.of(a), iter.of(b)); return ys.len(); }`},
	// take / skip → array collectors.
	{"take", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; var ys: i32[] = iter.take(iter.of(xs), 2); return ys.len(); }`},
	{"skip", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; var ys: i32[] = iter.skip(iter.of(xs), 2); return ys.len(); }`},
	// nth / last → Option in match scrutinee (no fn-value, but match-scrutinee).
	{"nth", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; match (iter.nth(iter.of(xs), 2)) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"last", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; match (iter.last(iter.of(xs))) { Some(v) => { return v; }, None => { return 0; } } }`},
	// position / count_value (i32-only Iterator helpers).
	{"position", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 3, 4]; match (iter.position(iter.of(xs), 3)) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"count_value", `import "core/iter";
function main(): i32 { var xs: i32[] = [1, 2, 2, 3]; return iter.count_value(iter.of(xs), 2); }`},
}

func TestSelfHostIterCombinatorsIR(t *testing.T) {
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

	for _, tc := range iterCombinatorIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "itercomb_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "itercomb_"+tc.name+"_bin", asm)
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
