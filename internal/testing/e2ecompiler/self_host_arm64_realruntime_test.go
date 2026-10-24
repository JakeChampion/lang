package e2ecompiler

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostArm64DarwinAssemblesRealRuntime(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin real-runtime e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "flatten.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/asm_ir_run.fern", "drivers/wasm_run.fern")
	darwinEmit := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "darwin_emit")
	wrun := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	native := arm64NativeSrc(t)

	cases := []struct{ name, src string }{
		{"exit42", `function main(): i32 { return 42; }`},
		{"arith", `function main(): i32 { let x = 6; let y = 7; return x * y; }`},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asmText := runCapture(t, gcc, runner, darwinEmit, []byte(c.src+"\n"), "-target", "arm64-darwin")
			if len(asmText) == 0 {
				t.Fatalf("darwin-asm emitter produced 0 bytes for %s", c.name)
			}

			var sb strings.Builder
			sb.WriteString(native)
			sb.WriteString("\nfunction main(): i32 {\n    let asm: string = \"\";\n")
			for _, ln := range strings.Split(string(asmText), "\n") {
				sb.WriteString("    asm = asm + \"" + fernEscapeAsmLine(ln) + "\\n\";\n")
			}
			sb.WriteString("    let p: Arm64GasProg = arm64_gas_program(asm);\n")
			sb.WriteString("    if (p.unknown.len() > 0) { write(\"UNKNOWN:\"); let ui: i32 = 0; while (ui < p.unknown.len()) { if (ui > 0) { write(\",\"); } write(p.unknown[ui]); ui = ui + 1; } return 0; }\n")
			sb.WriteString("    let pa: Arm64Asm = p.asm;\n")
			sb.WriteString("    let ehlen: i32 = arm64_eh_frame_darwin_len(p);\n")
			sb.WriteString("    let tv: i64 = macho_text_vaddr(pa.text.len(), ehlen, p.data.len(), p.bss_size);\n")
			sb.WriteString("    let ev: i64 = macho_eh_vaddr(pa.text.len(), ehlen, p.data.len(), p.bss_size);\n")
			sb.WriteString("    let dv: i64 = macho_data_vaddr(pa.text.len(), ehlen, p.data.len(), p.bss_size);\n")
			sb.WriteString("    let eh: i32[] = arm64_eh_frame_darwin(p, tv, ev);\n")
			sb.WriteString("    p = arm64_gas_link(p, tv, dv);\n")
			sb.WriteString("    let pa2: Arm64Asm = p.asm;\n")
			sb.WriteString("    let bin: i32[] = macho_executable(pa2.text, eh, p.data, \"fern\", macho_entry_off(pa2), p.bss_size, arm64_gas_rebase_offs(p));\n")
			sb.WriteString("    write(string_from_bytes_unchecked(to_u8(bin)));\n    return 0;\n}\n")

			wat := runCapture(t, gcc, runner, wrun, []byte(sb.String()))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes for the assemble driver")
			}
			watPath := filepath.Join(dir, c.name+"_asm.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			bin, err := exec.Command("wasmtime", "run", watPath).Output()
			if err != nil {
				t.Fatalf("wasmtime run (assemble driver): %v", err)
			}
			if bytes.HasPrefix(bin, []byte("UNKNOWN:")) {
				t.Fatalf("arm64_native could not assemble %s's real darwin output: %s", c.name, bin)
			}
			f, err := macho.NewFile(bytes.NewReader(bin))
			if err != nil {
				t.Fatalf("assembled output is not a parseable Mach-O: %v", err)
			}
			if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
				t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
			}
		})
	}
}
