package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var iterBoundedReducerIRCases = []struct {
	name string
	src  string
}{
	{"sum", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; return iter.sum(iter.of(xs)); }`},
	{"sum-empty", `import "core/iter";
function main(): i32 { let xs: i32[] = []; return iter.sum(iter.of(xs)); }`},
	{"product", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4]; return iter.product(iter.of(xs)); }`},
	{"len", `import "core/iter";
function main(): i32 { let xs: i32[] = [1, 2, 3, 4, 5]; return iter.of(xs).len(); }`},
	{"rest-len", `import "core/iter";
function rest[T, I: iter.Iterator[T]](it: I): T[] { return it.collect(); }
function main(): i32 { let xs: i32[] = [9, 8, 7]; let ys: i32[] = rest(iter.of(xs)); return ys.len(); }`},
	{"rest-elem", `import "core/iter";
function rest[T, I: iter.Iterator[T]](it: I): T[] { return it.collect(); }
function main(): i32 { let xs: i32[] = [9, 8, 7]; let ys: i32[] = rest(iter.of(xs)); return ys[1]; }`},
	{"range-sum", `import "core/iter";
function main(): i32 { return iter.sum(iter.range(1, 5)); }`},
	{"sum-i64-unannotated", `import "core/iter";
function main(): i32 { let xs: i64[] = [3000000000, 3000000000]; let s = iter.sum(iter.of(xs)); if (s == 6000000000) { return 7; } return 1; }`},
	{"user-qualified-bound", `import "core/iter";
import "std/num" as num;
function first_of[T: num.Add, I: iter.Iterator[T]](it: I): Option[T] {
  match (it.next()) { Some(t) => { return Some(t.0); }, None => { return None; } }
}
function main(): i32 { let xs: i64[] = [9, 2]; let f = first_of(iter.of(xs)); match (f) { Some(v) => { if (v == 9) { return 9; } return 1; }, None => { return 2; } } }`},
	{"contains-literal-i64", `import "core/iter";
function main(): i32 { let xs: i64[] = [3, 9, 2]; if (iter.contains(iter.of(xs), 2) && iter.count(iter.of(xs), 9) == 1) { return 5; } return 1; }`},
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
