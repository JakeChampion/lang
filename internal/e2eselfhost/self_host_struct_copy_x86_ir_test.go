package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// structCopyIRCases exercise the compact functional-struct-update lowering
// (op_struct_copy, #4650) on the self-host x86-64 IR path. `T { ...base, f: v }`
// on a struct_copy_eligible type (no nested-struct / enum / bare-string / 8-byte
// scalar field — i.e. only scalar + array / string[] / map / option / tuple
// fields) lowers its base copy to ONE `call __fn___fern_struct_copy` (a shallow
// word-for-word box copy) plus a struct_set per overridden field, instead of N
// inline struct_get/store pairs. Each case pins:
//   - exit code == VALUE correctness (a wrong copy / lost override diverges);
//   - copyAssert == the EMISSION contract: +1 requires ≥1 `call __fn___fern_struct_copy`
//     (the compact path fired), -1 requires zero (an ineligible struct must stay
//     on the inline path so the per-field retains still run).
var structCopyIRCases = []struct {
	name       string
	src        string
	expected   int
	copyAssert int // +1: must emit struct_copy; -1: must NOT; 0: don't check
}{
	// Threaded builder, scalar override on an array-field struct: base copy is
	// one struct_copy, `b` overridden. sum 0..9 = 45, +len(a)=2 +len(c)=1 = 48.
	{"scalar-override-threaded",
		`struct S { a: i32[], b: i32, c: i32[] } function bump(s: S, v: i32): S { return S { ...s, b: s.b + v }; } function main(): i32 { var s: S = S { a: [1, 2], b: 0, c: [9] }; var i: i32 = 0; while (i < 10) { s = bump(s, i); i = i + 1; } return s.b + s.a.len() + s.c.len(); }`,
		48, 1},
	// Array-field override (the immutable-update idiom `xs: xs.append(v)`): the
	// unchanged fields ride struct_copy, `a` gets the fresh appended array. After
	// 6 appends a.len()=2+6=8, b unchanged 5. 8 + 5 = 13.
	{"array-override",
		`struct S { a: i32[], b: i32 } function push(s: S, v: i32): S { return S { ...s, a: s.a.append(v) }; } function main(): i32 { var s: S = S { a: [1, 2], b: 5 }; var i: i32 = 0; while (i < 6) { s = push(s, i); i = i + 1; } return s.a.len() + s.b; }`,
		13, 1},
	// Multiple overrides in one update: two fields set, two copied via struct_copy.
	// a.len 3, b=7, c.len 1, d=100 -> 3+7+1+100 = 111.
	{"multi-override",
		`struct S { a: i32[], b: i32, c: i32[], d: i32 } function mk(s: S): S { return S { ...s, b: 7, d: 100 }; } function main(): i32 { var s: S = S { a: [1, 2, 3], b: 0, c: [9], d: 0 }; s = mk(s); return s.a.len() + s.b + s.c.len() + s.d; }`,
		111, 1},
	// string[] field is admitted by struct_copy_eligible (it is NOT retained on
	// the inline base copy), so the compact path still fires. names kept across
	// the update, count bumped. len(names)=2, count=3 -> 5.
	{"string-array-field",
		`struct S { names: string[], count: i32 } function inc(s: S): S { return S { ...s, count: s.count + 1 }; } function main(): i32 { var s: S = S { names: ["x", "y"], count: 0 }; var i: i32 = 0; while (i < 3) { s = inc(s); i = i + 1; } return s.names.len() + s.count; }`,
		5, 1},
	// NEGATIVE: a bare `string` field makes the struct ineligible (the inline base
	// copy retains the shared string), so struct_copy must NOT fire — the update
	// stays on the per-field path. Value must still be correct. tag "hi" len 2,
	// n=4 -> 6.
	{"bare-string-ineligible-no-copy",
		`struct S { tag: string, n: i32 } function bump(s: S): S { return S { ...s, n: s.n + 1 }; } function main(): i32 { var s: S = S { tag: "hi", n: 0 }; var i: i32 = 0; while (i < 4) { s = bump(s); i = i + 1; } return s.tag.len() + s.n; }`,
		6, -1},
}

func TestSelfHostStructCopyIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range structCopyIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			copies := bytes.Count(asm, []byte("call __fn___fern_struct_copy"))
			switch {
			case tc.copyAssert > 0 && copies == 0:
				t.Errorf("%s: expected op_struct_copy to fire (call __fn___fern_struct_copy), found none — compact update path regressed", tc.name)
			case tc.copyAssert < 0 && copies != 0:
				t.Errorf("%s: expected NO struct_copy (ineligible struct must stay inline), found %d", tc.name, copies)
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
