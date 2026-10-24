package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var arrmResidualRetIRCases = []struct {
	name string
	src  string
}{
	{"annotated-var", `import "std/array";
function f(xs: i64[]): i64 {
    let out: Option[i64] = xs.map((x: i64): i64 => x + (1 as i64)).reduce((a: i64, b: i64): i64 => a + b);
    match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return f([1 as i64, 2 as i64]) as i32; }`},
	{"unannotated-var", `import "std/array";
function f(xs: i64[]): i64 {
    let out = xs.map((x: i64): i64 => x + (1 as i64)).reduce((a: i64, b: i64): i64 => a + b);
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
	{"string-payload", `import "std/array";
import "std/string";
function f(xs: i64[]): i32 {
    let out: Option[string] = xs.map((x: i64): string => "a").reduce((a: string, b: string): string => a + b);
    match (out) { Some(v) => { return v.len(); }, None => { return 0; } }
}
function main(): i32 { return f([1 as i64, 2 as i64, 3 as i64]); }`},
	{"filter-map-reduce", `import "std/array";
function f(xs: i64[]): i64 {
    let out: Option[i64] = xs.filter((x: i64): boolean => x > (0 as i64))
                             .map((x: i64): i64 => x + (1 as i64))
                             .reduce((a: i64, b: i64): i64 => a + b);
    match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return f([1 as i64, 2 as i64, 3 as i64]) as i32; }`},
	{"control-reduce-alone", `import "std/array";
function f(xs: i64[]): i64 {
    let out: Option[i64] = xs.reduce((a: i64, b: i64): i64 => a + b);
    match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return f([1 as i64, 2 as i64]) as i32; }`},
	{"control-split-statements", `import "std/array";
function f(xs: i64[]): i64 {
    let ys: i64[] = xs.map((x: i64): i64 => x + (1 as i64));
    let out: Option[i64] = ys.reduce((a: i64, b: i64): i64 => a + b);
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
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "arrmres")
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

	for _, tc := range arrmResidualRetIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "arrmres_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")

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
