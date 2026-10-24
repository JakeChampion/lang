package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var arrayMethodMultiElemIRCases = []struct {
	name string
	src  string
	syms []string
}{
	{"map-i32-string", `import "std/array";
function dbl(x: i32): i32 { return x * 2; }
@noinline function id_s(s: string): string { return s; }
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let ss: string[] = ["a", "b"];
    let a: i32[] = xs.map(dbl);
    let b: string[] = ss.map(id_s);
    return a[2] + b.len();
}`, []string{"__arrm_map__i32", "__arrm_map__string"}},

	{"map-i32-string-i64", `import "std/array";
function dbl(x: i32): i32 { return x * 2; }
@noinline function id_s(s: string): string { return s; }
function id_l(x: i64): i64 { return x; }
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let ss: string[] = ["a", "b"];
    let ls: i64[] = [7i64];
    return xs.map(dbl)[2] + ss.map(id_s).len() + (ls.map(id_l)[0] as i32);
}`, []string{"__arrm_map__i32", "__arrm_map__string", "__arrm_map__i64"}},

	{"concat-i32-string", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2];
    let ys: i32[] = [3];
    let ss: string[] = ["a"];
    let ts: string[] = ["b", "c"];
    let a: i32[] = xs.concat(ys);
    let b: string[] = ss.concat(ts);
    return a.len() * 10 + b.len();
}`, []string{"__fern_arr_concat", "__arrm_concat__string"}},

	{"is_sorted-bounded", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let ss: string[] = ["b", "a"];
    let a: boolean = xs.is_sorted();
    let b: boolean = ss.is_sorted();
    if (a && !b) { return 7; }
    return 9;
}`, []string{"__arrm_is_sorted__i32", "__arrm_is_sorted__string"}},

	{"dedup-bounded", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 1, 2];
    let ss: string[] = ["a", "a", "b", "c"];
    return xs.dedup().len() * 10 + ss.dedup().len();
}`, []string{"__arrm_dedup__i32", "__arrm_dedup__string"}},

	{"rotate_left-silent-misdispatch", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let ss: string[] = ["a", "b", "c"];
    let a: i32[] = xs.rotate_left(1);
    let b: string[] = ss.rotate_left(1);
    return a[0] * 10 + b[0].len() + b.len();
}`, []string{"__arrm_rotate_left__i32", "__arrm_rotate_left__string"}},

	{"chained-pipeline", `import "std/array";
function dbl(x: i32): i32 { return x * 2; }
function big(x: i32): boolean { return x > 2; }
@noinline function id_s(s: string): string { return s; }
function nonempty(s: string): boolean { return s.len() > 0; }
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let ss: string[] = ["a", "", "b"];
    let a: i32[] = xs.map(dbl).filter(big);
    let b: string[] = ss.map(id_s).filter(nonempty);
    return a.len() * 10 + b.len();
}`, []string{"__arrm_map__i32", "__arrm_map__string", "__arrm_filter__"}},

	{"filter-narrow-and-wide", `import "std/array";
function bigi(x: i32): boolean { return x > 1; }
function bigl(x: i64): boolean { return x > 1i64; }
function bigf(x: f64): boolean { return x > 1.0; }
function nonempty(s: string): boolean { return s.len() > 0; }
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let ls: i64[] = [1i64, 5i64, 9i64];
    let fs: f64[] = [0.5, 2.5];
    let ss: string[] = ["a", "", "b"];
    let a = xs.filter(bigi);
    let b = ls.filter(bigl);
    let c = fs.filter(bigf);
    let d = ss.filter(nonempty);
    return a.len() * 1000 + b.len() * 100 + c.len() * 10 + d.len();
}`, []string{"__arrm_filter__"}},
}

func TestSelfHostArrayMethodMultiElemIR(t *testing.T) {
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
