package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var monoIndexArgIRCases = []struct {
	name string
	src  string
}{
	{"bounded-elem-args", `import "core/cmp";
function after[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function count_desc[T: cmp.Ord](a: T[]): i32 {
    let c: i32 = 0;
    let i: i32 = 1;
    while (i < a.len()) {
        if (after(a[i - 1], a[i])) { c = c + 1; }
        i = i + 1;
    }
    return c;
}
function main(): i32 { let xs: i32[] = [3, 1, 2]; return count_desc(xs); }`},

	{"bounded-elem-args-condition", `import "core/cmp";
function bigger[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function main(): i32 {
    let xs: i32[] = [9, 4];
    if (bigger(xs[0], xs[1])) { return 7; }
    return 3;
}`},

	{"bounded-elem-args-string", `import "core/cmp";
function bigger[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function main(): i32 {
    let xs: string[] = ["b", "a"];
    if (bigger(xs[0], xs[1])) { return 7; }
    return 3;
}`},

	{"typevar-elem-stays-generic", `import "std/array";
import "core/cmp";
function bigger[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    if (bigger(array.reverse(xs)[0], 1)) { return 7; }
    return 3;
}`},

	{"method-result-args", `import "core/cmp";
struct S { n: u32 }
function (s: S) finish(): u32 { return s.n * 2u32; }
function bigger[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function main(): i32 {
    let a: S = S { n: 4u32 };
    let b: S = S { n: 3u32 };
    if (bigger(a.finish(), b.finish())) { return 7; }
    return 3;
}`},

	{"method-result-args-imported", `import "core/cmp";
import "std/hash" as hash;
function bigger[T: cmp.Ord](x: T, y: T): boolean { return x.cmp(y) > 0; }
function main(): i32 {
    let s: hash.SysvSum = hash.sysv_sum_new().update("abc".as_bytes());
    let e: hash.SysvSum = hash.sysv_sum_new();
    if (bigger(s.finish(), e.finish())) { return 7; }
    return 3;
}`},
}

func TestSelfHostMonoIndexArgIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "monoidx")
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

	for _, tc := range monoIndexArgIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "monoidx_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\" (the un-monomorphised call bailed the module)",
					tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "monoidx_"+tc.name+"_bin", asm)
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
