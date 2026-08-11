package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStrbufBufferX86_64 guards the self-host AST emitter's strbuf
// output buffer. The emitted asm accumulates into a fixed `__fern_strbuf_data`
// .bss buffer; the self-host's OWN self-compile output is ~16.7 MB, which had
// already reached 99.9% of the previous 16 MiB buffer — so any addition to the
// compiler (e.g. growing a pervasive struct like ParamDecl) tipped the output
// past the buffer and `__fern_strbuf_append` silently ran into adjacent .bss
// and segfaulted later (a 0-byte, hard-to-diagnose crash). This pins the two
// halves of the fix: the buffer is large enough (64 MiB) AND `strbuf_append`
// bounds-checks so a future overflow traps cleanly (exit 137) instead of
// corrupting memory. A directly-overflowing test is impractical (it would need
// a program whose emitted asm exceeds 64 MiB), so this asserts the structure.
func TestSelfHostStrbufBufferX86_64(t *testing.T) {
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

	// A program that uses the strbuf builtins directly, so asm.fern emits the
	// strbuf runtime (the buffer + append/reset/take) into the output asm.
	asm := runCapture(t, gcc, runner, driverBin, []byte(`function main(): i32 { strbuf_reset(); strbuf_append("ab"); var s = strbuf_take(); return s.len(); }`))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	out := string(asm)
	// The buffer must be the enlarged 64 MiB (not the old 16 MiB = 16777216).
	if !strings.Contains(out, "__fern_strbuf_data: .skip 67108864") {
		t.Errorf("strbuf output buffer is not 64 MiB; the self-host self-compile output (~16.7 MB) needs the headroom")
	}
	if strings.Contains(out, "__fern_strbuf_data: .skip 16777216") {
		t.Errorf("strbuf output buffer is still the old 16 MiB — too small for the self-host's own output")
	}
	// strbuf_append must bounds-check against the buffer end and trap.
	if !strings.Contains(out, "cmpq $67108864") {
		t.Errorf("strbuf_append has no bounds check against the 64 MiB buffer end; an overflow would silently corrupt .bss")
	}
}

// TestSelfHostStrbufBufferArm64 is the arm64 counterpart — the arm64 emitter
// cross-emits on x86, so this only inspects the emitted asm (no qemu needed).
func TestSelfHostStrbufBufferArm64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	asm := runCapture(t, gcc, runner, driverBin, []byte(`function main(): i32 { strbuf_reset(); strbuf_append("ab"); var s = strbuf_take(); return s.len(); }`), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	out := string(asm)
	if !strings.Contains(out, "__fern_strbuf_data: .skip 67108864") {
		t.Errorf("arm64 strbuf output buffer is not 64 MiB")
	}
	if !strings.Contains(out, "movz x5, #0x400, lsl #16") {
		t.Errorf("arm64 strbuf_append has no bounds check against the 64 MiB buffer end")
	}
}
