package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8427: a struct rebuilt over an `own` base ran rc traffic over its unsigned
// scalar fields. is_enum_like_name reads `u32` / `u64` as a bare nominal
// enum, and both struct-reuse emitters keyed on it: the reuse arm released
// the OLD value of an overridden `total: u64` with __fern_rc_dec (a byte
// count that, once past the low-address guard, is written through — a
// SIGSEGV after ~70 thousand-byte updates on x86-64), and the fresh arm
// retained the copied field and moved it through a width-64 struct_get,
// which the wasm emitter renders as f64.load / f64.store (rejected by the
// validator: expected i32, found f64). The i64 override temp was untyped too.
//
// The cases are the streaming-hasher absorb shape that surfaced it, sized so
// the counter crosses the guard, and every expectation is the interpreter's.
var selfHostStructUpdateScalarFieldCases = []struct {
	name string
	src  string
}{
	// The return form over an `own` base: the reuse arm's old-field release.
	{"own-return-spread-u64", "struct St { a: u32, buf: u8[], buf_len: i32, total: u64 }\n" +
		"function absorb(own h: St, bs: [u8]): St {\n    var n: i32 = bs.len();\n    return St { ...h, buf_len: n, total: h.total + (n as u64) };\n}\n" +
		"function (h: St) update(chunk: string): St { h = absorb(h, chunk.as_bytes()); return h; }\n" +
		"function main(): i32 {\n    var b: u8[] = __alloc_u8(1000);\n    var i: i32 = 0;\n    while (i < 1000) { b = b.with(i, ((i * 7) & 255) as u8); i = i + 1; }\n" +
		"    var chunk: string = string_from_bytes_unchecked(b);\n    var h: St = St { a: 1, buf: __alloc_u8(64), buf_len: 0, total: 0 as u64 };\n" +
		"    var k: i32 = 0;\n    while (k < 100) { h = h.update(chunk); k = k + 1; }\n" +
		"    return ((h.total / (1000 as u64)) as i32) + h.buf_len / 100 + __rc_underflow_count() * 100;\n}"},
	// The rebind form with a field moved into an `own` helper: the fresh arm's
	// non-overridden-field copy (the wasm f64 site) and the i64 override temp.
	{"own-rebind-field-move-u64", "struct St { a: u32, buf: u8[], buf_len: i32, total: u64 }\n" +
		"function cp(own dst: u8[], at: i32, src: [u8], from: i32, len: i32): u8[] {\n    var i: i32 = 0;\n    while (i < len) { dst = dst.with(at + i, src[from + i]); i = i + 1; }\n    return dst;\n}\n" +
		"function absorb(own h: St, bs: [u8]): St {\n    var n: i32 = bs.len();\n    var bl: i32 = h.buf_len;\n    var take: i32 = 64 - bl;\n    if (take > n) { take = n; }\n" +
		"    h = St { ...h, buf: cp(h.buf, bl, bs, 0, take), buf_len: (bl + take) % 64, total: h.total + (n as u64) };\n    return h;\n}\n" +
		"function (h: St) update(chunk: string): St { h = absorb(h, chunk.as_bytes()); return h; }\n" +
		"function main(): i32 {\n    var b: u8[] = __alloc_u8(1000);\n    var i: i32 = 0;\n    while (i < 1000) { b = b.with(i, ((i * 7) & 255) as u8); i = i + 1; }\n" +
		"    var chunk: string = string_from_bytes_unchecked(b);\n    var h: St = St { a: 1, buf: __alloc_u8(64), buf_len: 0, total: 0 as u64 };\n" +
		"    var k: i32 = 0;\n    while (k < 100) { h = h.update(chunk); k = k + 1; }\n" +
		"    return ((h.total / (1000 as u64)) as i32) - 90 + (h.buf[3] as i32) % 7 + h.buf_len / 8 + __rc_underflow_count() * 100;\n}"},
}

// TestSelfHostStructUpdateScalarFieldsX86_64 — the production x86-64 IR path
// against the interpreter oracle.
func TestSelfHostStructUpdateScalarFieldsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostStructUpdateScalarFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "susf_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostStructUpdateScalarFieldsWasmIR — the wasm-IR leg, where the
// width-64 field copy has to validate as i64 before anything can run.
func TestSelfHostStructUpdateScalarFieldsWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR struct-update scalar-field e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostStructUpdateScalarFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin, "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("wasm driver failed: %v", err)
			}
			watFile := filepath.Join(dir, "susf_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			out, _ := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally:\n%s", out)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)\n%s", tc.name, code, want, out)
			}
		})
	}
}
