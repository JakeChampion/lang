package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var boundedArrayMethodIRCases = []struct {
	name string
	src  string
}{
	{"i32-peak", `import "core/cmp";
pub function (xs: T[]) peak[T: cmp.Ord](): Option[T] {
    if (xs.len() == 0) { return None; }
    let m: T = xs[0];
    let i: i32 = 1;
    while (i < xs.len()) { if (xs[i].cmp(m) > 0) { m = xs[i]; } i = i + 1; }
    return Some(m);
}
function main(): i32 { let a: i32[] = [3, 7, 2]; match (a.peak()) { Some(v) => { return v; }, None => { return 0; } } }`},
	{"f64-peak", `import "core/cmp";
pub function (xs: T[]) peak[T: cmp.Ord](): Option[T] {
    if (xs.len() == 0) { return None; }
    let m: T = xs[0];
    let i: i32 = 1;
    while (i < xs.len()) { if (xs[i].cmp(m) > 0) { m = xs[i]; } i = i + 1; }
    return Some(m);
}
function main(): i32 { let b: f64[] = [1.5, 2.5, 0.5]; match (b.peak()) { Some(v) => { return 1; }, None => { return 0; } } }`},
	{"string-peak", `import "core/cmp";
pub function (xs: T[]) peak[T: cmp.Ord](): Option[T] {
    if (xs.len() == 0) { return None; }
    let m: T = xs[0];
    let i: i32 = 1;
    while (i < xs.len()) { if (xs[i].cmp(m) > 0) { m = xs[i]; } i = i + 1; }
    return Some(m);
}
function main(): i32 { let s: string[] = ["pear", "apple", "zed"]; match (s.peak()) { Some(v) => { return v.len(); }, None => { return 0; } } }`},
	{"empty-none", `import "core/cmp";
pub function (xs: T[]) peak[T: cmp.Ord](): Option[T] {
    if (xs.len() == 0) { return None; }
    let m: T = xs[0];
    let i: i32 = 1;
    while (i < xs.len()) { if (xs[i].cmp(m) > 0) { m = xs[i]; } i = i + 1; }
    return Some(m);
}
function main(): i32 { let e: i32[] = []; match (e.peak()) { Some(v) => { return 1; }, None => { return 42; } } }`},
}

func TestSelfHostBoundedArrayMethodIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "balr")
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

	for _, tc := range boundedArrayMethodIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "boundam_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "boundam_"+tc.name+"_bin", asm)
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
