package e2eselfhost

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64NativeViaGoBackend drives the flipped `-target
// arm64-darwin` path under the *Go x86-64 reference backend* — the backend
// the unified `fern` CLI is built with on the Linux CI box. It builds the
// CLI from fern.fern (which now `import`s arm64_native and assembles +
// links the Mach-O in-process — no clang/ld64), emits a binary for each
// program, and asserts a valid arm64 MH_EXECUTE. The flagship
// TestSelfHostArm64DarwinBuilds additionally *executes* the binaries on the
// macOS arm64 runner; this is the Linux-side structural guard.
//
// The cases deliberately span the instruction forms a real compiler emits:
// string concat (the signed-offset `stp/ldp [sp, #off]` large-frame forms),
// i64 math (`sxtw`), and bitwise ops (the register `lsl/lsr/asr` shifts) —
// forms originally unhandled by arm64_native (the `ldp` offset form indexed
// ops[3] out of range, a bounds abort that *looked* like a Go x86 backend
// miscompile but was a real assembler gap; `stp [sp,#off]` was silently
// mis-encoded as pre-index; the register shifts / `sxtw` were missing). See
// SELF-HOST-REMAINING-PLAN.md (slice 3p) and the byte-pinned
// TestSelfHostArm64OffsetPairGas.
//
// (This also guards the struct spread-update self-overwrite miscompile the
// flip surfaced: `p = T { ...p, f: v }` of a parameter mis-lowered in the
// shared internal/ir FBIP-reuse fast path. It was originally worked around
// with per-function local copies in arm64_native; those are gone now that the
// IR bug is fixed (slice 3r / TestStructUpdateParamSpreadReuse), and this test
// would surface a regression of that fix when arm64_native is compiled by the
// Go x86 backend.)
func TestSelfHostArm64NativeViaGoBackend(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("native x86-64 run required")
	}

	// Stage the full self-host project + fern.fern's transitive closure
	// (incl. arm64_native.fern, which the flipped fern.fern now imports).
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asmcore.fern", "util.fern", "flatten.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "checker.fern", "interp.fern", "printer.fern", "astwalk.fern", "ssa.fern", "ssa_arm64.fern", "ssa_x86.fern", "ssa_wasm.fern", "watbin.fern", "constfold.fern", "arm64_native.fern", "elf.fern", "fern.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	cases := []struct{ name, src string }{
		{"exit42", `function main(): i32 { return 42; }`},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`},
		{"print", `function main(): i32 { print("hi"); return 0; }`},
		{"concat", `function main(): i32 { var s: string = "a"; s = s + "b"; print(s); return 0; }`},
		{"i64math", `function main(): i32 { var a: i64 = 1000000; var b: i64 = 7; var c: i64 = a*b + a/b; return (c % 256) as i32; }`},
		{"bitwise", `function main(): i32 { var a: i32 = 240; var b: i32 = 15; var c: i32 = (a & b) | (a << 2); var d: i32 = c >> 1; return d % 256; }`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			binPath := filepath.Join(dir, c.name+".bin")
			if out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", binPath, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("self-host emit (arm64-darwin binary) failed: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("read bin: %v", err)
			}
			f, err := macho.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("emitted output is not a parseable Mach-O: %v (len=%d)", err, len(raw))
			}
			if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
				t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
			}
		})
	}
}
