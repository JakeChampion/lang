package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var structRCIRCases = []struct {
	name       string
	src        string
	expected   int
	freeAssert int
	body       string
}{
	{"loop-rebind-reclaimed",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5) { let p: P = P { x: i, y: i + 1 }; sum = sum + p.x + p.y; i = i + 1; } return sum; }`,
		25, 1, ""},
	{"single-reclaimed",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 30, y: 12 }; return p.x + p.y; }`,
		42, -1, "main"},
	{"single-runtime-reclaimed",
		`struct P { x: i32, y: i32 } @noinline function probe(n: i32): i32 { let p: P = P { x: n, y: 12 }; return p.x + p.y; } function main(): i32 { return probe(args().len() + 29); }`,
		42, 1, "probe"},
	{"borrow-call-arg-reclaimed",
		`struct P { x: i32, y: i32 } function sumit(p: P): i32 { return p.x + p.y; } function main(): i32 { let total: i32 = 0; let i: i32 = 0; while (i < 3) { let p: P = P { x: i, y: 10 }; total = total + sumit(p); i = i + 1; } return total; }`,
		33, 1, ""},
	{"escape-return-survives",
		`struct P { x: i32, y: i32 } function make(): P { let p: P = P { x: 7, y: 9 }; return p; } function main(): i32 { let q: P = make(); return q.x + q.y; }`,
		16, 0, ""},
	{"escape-struct-field-value",
		`struct Inner { a: i32 } struct Outer { v: i32, w: i32 } function main(): i32 { let q: Inner = Inner { a: 5 }; let r: Outer = Outer { v: q.a, w: 8 }; return r.v + r.w; }`,
		13, 0, ""},
}

func TestSelfHostStructRCIRX86_64(t *testing.T) {
	boxedProbes(t)
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

	for _, tc := range structRCIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			checked := asm
			if tc.body != "" {
				checked = []byte(selfHostFnBody(t, asm, tc.body))
				if bytes.Contains(checked, []byte("call __fern_arr_box")) != (tc.freeAssert > 0) {
					t.Errorf("%s: allocation disagrees with static/runtime struct:\n%s", tc.name, checked)
				}
				if tc.freeAssert < 0 && !bytes.Contains(checked, []byte(".K0(%rip)")) {
					t.Errorf("%s: missing static struct load:\n%s", tc.name, checked)
				}
			}
			frees := bytes.Count(checked, []byte("call __fn___fern_arr_dec"))
			switch {
			case tc.freeAssert > 0 && frees == 0:
				t.Errorf("%s: expected a struct free (call __fn___fern_arr_dec), found none — regressed to leak-only", tc.name)
			case tc.freeAssert < 0 && frees != 0:
				t.Errorf("%s: expected no static struct free, found %d", tc.name, frees)
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
