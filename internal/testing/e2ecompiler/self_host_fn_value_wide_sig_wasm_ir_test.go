package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var fnValueWideSigCases = []struct {
	name string
	src  string
}{
	{"wide-param-loop", `function sweep(n: i64, prop: (i64) => boolean): i64 {
    let s: i64 = 1;
    while (s <= n) {
        if (!prop(s)) { return s; }
        s = s + 1;
    }
    return 0;
}
function main(): i32 { return sweep(5 as i64, (x: i64) => x < 3) as i32; }`},
	{"wide-result", `function apply(g: (i32) => i64): i64 { return g(5); }
function main(): i32 { return (apply((x: i32) => (x as i64) * 3000000000i64) / 3000000000i64) as i32; }`},
	{"wide-param-and-result", `function apply(g: (i64) => i64, v: i64): i64 { return g(v); }
function main(): i32 { return (apply((x: i64) => x * 2i64, 21i64)) as i32; }`},
	{"f64-param", `function pick(g: (f64) => i32, v: f64): i32 { return g(v); }
function main(): i32 { return pick((x: f64) => (x * 2.0) as i32, 21.0); }`},
	{"wide-result-bound", `function apply(g: (i32) => i64): i64 { let v: i64 = g(5); return v + 1i64; }
function main(): i32 { return (apply((x: i32) => (x as i64) * 3000000000i64) / 1000000000i64) as i32; }`},
	{"all-i32-control", `@noinline
function apply(g: (i32) => i32): i32 { return g(5); }
function main(): i32 { return apply((x: i32) => x * 2); }`},

	{"shadowed-sig", `function apply(v: i64): i64 {
    let g: (i64) => i64 = (x: i64) => x * 2i64;
    let t: i64 = g(v);
    if (v > 0i64) {
        let g: (i32) => i32 = (y: i32) => y + 1;
        t = t + (g(3) as i64);
    }
    return t + g(v + 1i64);
}
function main(): i32 { return apply(20i64) as i32; }`},
	{"shadowed-sig-rename-control", `function apply(v: i64): i64 {
    let g: (i64) => i64 = (x: i64) => x * 2i64;
    let t: i64 = g(v);
    if (v > 0i64) {
        let h: (i32) => i32 = (y: i32) => y + 1;
        t = t + (h(3) as i64);
    }
    return t + g(v + 1i64);
}
function main(): i32 { return apply(20i64) as i32; }`},

	{"shadowed-ret", `function apply(v: i64): i64 {
    let g: (i32) => i64 = (x: i32) => (x as i64) * 3000000000i64;
    let t: i64 = g(1);
    if (v > 0i64) {
        let g: (i32) => i32 = (y: i32) => y + 7;
        let u: i64 = g(3) as i64;
        t = t + u;
    }
    return (t / 1000000000i64) + g(2);
}
function main(): i32 { return (apply(4i64) % 83i64) as i32; }`},
	{"shadowed-ret-rename-control", `function apply(v: i64): i64 {
    let g: (i32) => i64 = (x: i32) => (x as i64) * 3000000000i64;
    let t: i64 = g(1);
    if (v > 0i64) {
        let h: (i32) => i32 = (y: i32) => y + 7;
        let u: i64 = h(3) as i64;
        t = t + u;
    }
    return (t / 1000000000i64) + g(2);
}
function main(): i32 { return (apply(4i64) % 83i64) as i32; }`},

	{"shadowed-dyn", `trait Show { function show(self: Self): i32; }
struct A { v: i32 }
impl Show for A { function show(self: Self): i32 { return self.v; } }
function run(k: i32): i32 {
    let d: (dyn Show) => i32 = (s: dyn Show) => s.show();
    let t: i32 = d(A { v: k });
    if (k % 2 == 0) {
        let d: (i32) => i32 = (y: i32) => if (y == 4) { 1 } else { 0 };
        t = t + d(4);
    }
    return t + d(A { v: 1 });
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + run(i); i = i + 1; }
    return t % 83;
}`},
	{"shadowed-dyn-rename-control", `trait Show { function show(self: Self): i32; }
struct A { v: i32 }
impl Show for A { function show(self: Self): i32 { return self.v; } }
function run(k: i32): i32 {
    let d: (dyn Show) => i32 = (s: dyn Show) => s.show();
    let t: i32 = d(A { v: k });
    if (k % 2 == 0) {
        let e: (i32) => i32 = (y: i32) => if (y == 4) { 1 } else { 0 };
        t = t + e(4);
    }
    return t + d(A { v: 1 });
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + run(i); i = i + 1; }
    return t % 83;
}`},
}

func TestSelfHostFnValueWideSigWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fn-value wide-signature wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir,
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range fnValueWideSigCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}

			if tc.name == "all-i32-control" && !strings.Contains(string(wat), "(type $fn2 ") {
				t.Errorf("all-i32 control no longer emits the arity-keyed $fn2 type; the fallback moved")
			}

			rcmd := exec.Command("wasmtime", "run", watFile)
			var so, se bytes.Buffer
			rcmd.Stdout, rcmd.Stderr = &so, &se
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally (invalid module or a trap — the bug):\n%s", se.String())
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s exited %d, want %d (interp oracle)\nstderr: %s", tc.name, got, want, se.String())
			}
		})
	}
}

func TestSelfHostFnValueWideSigIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range fnValueWideSigCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)

			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
