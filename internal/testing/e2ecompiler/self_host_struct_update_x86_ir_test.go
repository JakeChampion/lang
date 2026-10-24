package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var structUpdateArrayFieldCases = []struct {
	name     string
	src      string
	expected int
}{
	{"scalar-override-threaded",
		`struct S { a: i32[], b: i32, c: i32[] } function bump(s: S, v: i32): S { return S { ...s, b: s.b + v }; } function main(): i32 { let s: S = S { a: [1, 2], b: 0, c: [9] }; let i: i32 = 0; while (i < 10) { s = bump(s, i); i = i + 1; } return s.b + s.a.len() + s.c.len(); }`,
		48},
	{"array-override",
		`struct S { a: i32[], b: i32 } function push(s: S, v: i32): S { return S { ...s, a: s.a.append(v) }; } function main(): i32 { let s: S = S { a: [1, 2], b: 5 }; let i: i32 = 0; while (i < 6) { s = push(s, i); i = i + 1; } return s.a.len() + s.b; }`,
		13},
	{"multi-override",
		`struct S { a: i32[], b: i32, c: i32[], d: i32 } function mk(s: S): S { return S { ...s, b: 7, d: 100 }; } function main(): i32 { let s: S = S { a: [1, 2, 3], b: 0, c: [9], d: 0 }; s = mk(s); return s.a.len() + s.b + s.c.len() + s.d; }`,
		111},
	{"string-array-field",
		`struct S { names: string[], count: i32 } function inc(s: S): S { return S { ...s, count: s.count + 1 }; } function main(): i32 { let s: S = S { names: ["x", "y"], count: 0 }; let i: i32 = 0; while (i < 3) { s = inc(s); i = i + 1; } return s.names.len() + s.count; }`,
		5},
	{"bare-string-field",
		`struct S { tag: string, n: i32 } function bump(s: S): S { return S { ...s, n: s.n + 1 }; } function main(): i32 { let s: S = S { tag: "hi", n: 0 }; let i: i32 = 0; while (i < 4) { s = bump(s); i = i + 1; } return s.tag.len() + s.n; }`,
		6},
}

func TestSelfHostStructUpdateIRX86_64(t *testing.T) {
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

	for _, tc := range structUpdateArrayFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if strings.Contains(string(asm), "__fern_struct_copy") {
				t.Error("struct update still emits the retired copy helper")
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
