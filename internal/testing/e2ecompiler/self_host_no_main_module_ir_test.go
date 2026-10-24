package e2ecompiler

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

var noMainModuleCases = []struct {
	name string
	src  string
}{
	{"single-fn", `pub function add(a: i32, b: i32): i32 { return a + b; }`},
	{"internal-call", `
function twice(n: i32): i32 { return n * 2; }
pub function quad(n: i32): i32 { return twice(twice(n)); }`},
	{"string-heap", `
pub function label(i: i32): string {
    if (i % 2 == 0) { return "ab" + "!"; }
    return "xyz";
}
pub function total(xs: i32[]): i32 {
    let t: i32 = 0;
    for x in xs { t = t + x; }
    return t;
}`},
	{"struct-and-enum", `
struct P { name: string, n: i32 }
enum C { Red, Blue }
pub function pick(p: P): i32 { return p.n + p.name.len(); }
pub function code(c: C): i32 { match (c) { Red => { return 1; }, Blue => { return 2; } } }`},
}

func TestSelfHostNoMainModuleIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range noMainModuleCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(strings.TrimLeft(tc.src, "\n") + "\n")
			if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src))); path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if strings.Contains(string(asm), "call __fn_main") {
				t.Errorf("%s: emitted `call __fn_main` for a module with no main", tc.name)
			}
			progBin := buildBin(t, gcc, dir, "nomain_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 (a main-less module's _start exits 0)", tc.name, code)
			}
		})
	}

	t.Run("typeinfo-fern-routes-ir", func(t *testing.T) {
		src, err := os.ReadFile("../../../compiler/typeinfo.fern")
		if err != nil {
			t.Fatalf("read typeinfo.fern: %v", err)
		}
		if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src))); path != "ir" {
			t.Fatalf("typeinfo.fern routed through %q path, want \"ir\"", path)
		}
		if asm := runCapture(t, gcc, runner, driverBin, src); len(asm) == 0 {
			t.Fatal("typeinfo.fern emitted 0 bytes")
		}
	})
}
