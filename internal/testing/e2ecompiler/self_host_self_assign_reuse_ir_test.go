package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var selfAssignReuseCases = []struct {
	name     string
	src      string
	expected int
}{
	{"self-assign-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 0, y: 7 }; let i: i32 = 0; while (i < 4) { p = P { ...p, x: p.x + 1 }; i = i + 1; } return p.x + p.y; }`,
		11},
	{"self-assign-reads-base",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 0, y: 7 }; let i: i32 = 0; while (i < 4) { p = P { ...p, x: p.x + p.y }; i = i + 1; } return p.x % 200; }`,
		28},
	{"self-assign-field-swap",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 1, y: 2 }; p = P { ...p, x: p.y, y: p.x }; return p.x * 10 + p.y; }`,
		21},
	{"self-assign-aliased",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 1, y: 2 }; let q: P = p; p = P { ...p, x: 9 }; return q.x * 10 + p.x + q.y; }`,
		21},
	{"self-assign-escapes-call",
		`struct P { x: i32, y: i32 } function take(q: P): i32 { return q.x + q.y; } function main(): i32 { let p: P = P { x: 1, y: 2 }; p = P { ...p, x: 5 }; return take(p); }`,
		7},
	{"self-assign-escapes-container",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 1, y: 2 }; let ps: P[] = []; p = P { ...p, x: 5 }; ps = ps.append(p); p = P { ...p, x: 9 }; return ps[0].x * 10 + p.x; }`,
		59},
	{"self-assign-plain-rebind",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 1, y: 2 }; p = P { ...p, x: 5 }; p = P { x: 100, y: 200 }; p = P { ...p, y: p.y + 1 }; return (p.x + p.y) % 200; }`,
		101},
	{"self-assign-array-field",
		`struct P { xs: i32[], n: i32 } function main(): i32 { let p: P = P { xs: [1, 2, 3], n: 0 }; let i: i32 = 0; while (i < 5) { p = P { ...p, xs: [i, i + 1], n: p.n + p.xs[0] }; i = i + 1; } return p.n + p.xs[0] + p.xs[1]; }`,
		16},
	{"self-assign-branch-arms",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 1, y: 2 }; let i: i32 = 0; while (i < 3) { if (i > 0) { p = P { ...p, x: p.x + p.y }; } else { p = P { ...p, y: p.y + 1 }; } i = i + 1; } return p.x * 10 + p.y; }`,
		73},
	{"self-assign-nested-struct",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { let p: Outer = Outer { inner: Inner { a: 1, b: 2 }, n: 0 }; let i: i32 = 0; while (i < 4) { p = Outer { ...p, inner: Inner { a: p.inner.a + 1, b: 3 }, n: p.n + p.inner.b }; i = i + 1; } return p.inner.a * 10 + p.n; }`,
		61},
	{"self-assign-shadowed-by-for",
		`struct P { xs: i32[], n: i32 } function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let p: P = P { xs: [1], n: 0 }; let ps: P[] = [P { xs: id([7, 8]), n: 5 }]; for p in ps { p = P { ...p, xs: [2], n: p.n + 1 }; } return ps[0].xs[0] * 10 + ps[0].n + p.n; }`,
		75},
	{"self-assign-shadowed-by-var",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 1, y: 2 }; let i: i32 = 0; while (i < 2) { let p: P = P { x: 5, y: 6 }; p = P { ...p, x: p.x + 1 }; i = i + 1; } p = P { ...p, x: p.x + 10 }; return p.x * 10 + p.y; }`,
		112},
	{"self-assign-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 0, y: 1 }; let i: i32 = 0; while (i < 5000000) { p = P { ...p, x: (p.x + p.y) % 1000 }; i = i + 1; } return p.x % 200; }`,
		0},
	{"self-assign-array-churn-safe",
		`struct P { xs: i32[], n: i32 } function main(): i32 { let p: P = P { xs: [1], n: 0 }; let i: i32 = 0; while (i < 200000) { p = P { ...p, xs: [i, i + 1, i + 2], n: (p.n + p.xs[0]) % 977 }; i = i + 1; } return p.n % 200; }`,
		8},
}

func TestSelfHostSelfAssignReuseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range selfAssignReuseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostSelfAssignReuseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host self-assign-reuse wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range selfAssignReuseCases {
		if strings.HasSuffix(tc.name, "churn-safe") {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("self-assign reuse wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
