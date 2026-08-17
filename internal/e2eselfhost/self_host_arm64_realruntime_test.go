package e2eselfhost

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostArm64DarwinAssemblesRealRuntime is the coverage milestone for
// the arm64-darwin native-binary path: it proves arm64_native.fern can
// assemble the self-host asm_arm64 emitter's *actual* darwin output — the
// whole runtime (the alloc bump heap, the i64/f64 helpers, the syscall
// shims) that asm_arm64 emits for every program — into a valid Mach-O, with
// zero unrecognised instructions.
//
// Pipeline: build the darwin-asm emitter (asm_ir_run.fern -target arm64-darwin,
// Go x86 backend) and the wasm_run driver; for each program, the emitter prints
// the darwin assembly TEXT (the unified `fern` CLI's `-target arm64-darwin` no
// longer emits text — it assembles the Mach-O in-process — so the dedicated
// emitter driver is how this test gets the text). That text is fed to a driver
// compiled through the self-host *wasm* emitter that runs it through
// arm64_gas_program + arm64_gas_link + macho_executable and writes
// the resulting Mach-O. This is the wasm-backend coverage of arm64_native
// assembling the full real runtime (the flagship TestSelfHostArm64DarwinBuilds
// covers the Go x86 / Go arm64 CLI path end-to-end). The test asserts the
// assembler reported no unknown mnemonics and the bytes parse as an arm64
// MH_EXECUTE.
func TestSelfHostArm64DarwinAssemblesRealRuntime(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin real-runtime e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "asm_ir_run.fern", "wasm_run.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	darwinEmit := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "darwin_emit")
	wrun := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	native := arm64NativeSrc(t)

	cases := []struct{ name, src string }{
		{"exit42", `function main(): i32 { return 42; }`},
		{"arith", `function main(): i32 { var x = 6; var y = 7; return x * y; }`},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asmText := runCapture(t, gcc, runner, darwinEmit, []byte(c.src+"\n"), "-target", "arm64-darwin")
			if len(asmText) == 0 {
				t.Fatalf("darwin-asm emitter produced 0 bytes for %s", c.name)
			}

			// Driver: assemble the real darwin asm via arm64_native; if any
			// mnemonic is unrecognised, print "UNKNOWN:<list>"; else emit the
			// Mach-O bytes.
			var sb strings.Builder
			sb.WriteString(native)
			sb.WriteString("\nfunction main(): i32 {\n    var asm: string = \"\";\n")
			for _, ln := range strings.Split(string(asmText), "\n") {
				sb.WriteString("    asm = asm + \"" + fernEscapeAsmLine(ln) + "\\n\";\n")
			}
			sb.WriteString("    var p: Arm64GasProg = arm64_gas_program(asm);\n")
			// Loop-write, not `.join` — see asmToMachoDriver: join forces the
			// driver onto the AST-fallback wasm backend, which miscompiles the
			// `p = arm64_gas_link(p, …)` rebind and crashes the signature hash.
			sb.WriteString("    if (p.unknown.len() > 0) { write(\"UNKNOWN:\"); var ui: i32 = 0; while (ui < p.unknown.len()) { if (ui > 0) { write(\",\"); } write(p.unknown[ui]); ui = ui + 1; } return 0; }\n")
			sb.WriteString("    var pa: Arm64Asm = p.asm;\n")
			sb.WriteString("    var tv: i64 = macho_text_vaddr(pa.code.len(), p.data.len(), p.bss_size);\n")
			sb.WriteString("    var dv: i64 = macho_data_vaddr(pa.code.len(), p.data.len(), p.bss_size);\n")
			sb.WriteString("    p = arm64_gas_link(p, tv, dv);\n")
			sb.WriteString("    var pa2: Arm64Asm = p.asm;\n")
			sb.WriteString("    var bin: i32[] = macho_executable(pa2.code, p.data, \"fern\", macho_entry_off(pa2), p.bss_size, arm64_gas_rebase_offs(p));\n")
			sb.WriteString("    write(string_from_bytes_unchecked(bin));\n    return 0;\n}\n")

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
