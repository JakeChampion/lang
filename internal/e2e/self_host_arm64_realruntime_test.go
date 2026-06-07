package e2e

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
// Pipeline: build the self-host CLI (Go x86 backend) and the wasm_run
// driver; for each program, `fern -target arm64-darwin` emits the darwin
// assembly TEXT (still the .s path on main); that text is fed to a driver
// compiled through the self-host wasm emitter that runs it through
// arm64_gas_program + arm64_gas_link + macho_static_executable and writes
// the resulting Mach-O. The test asserts the assembler reported no unknown
// mnemonics and the bytes parse as an arm64 MH_EXECUTE.
func TestSelfHostArm64DarwinAssemblesRealRuntime(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin real-runtime e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "asm_arm64.fern", "wasm.fern", "checker.fern", "interp.fern", "printer.fern", "ssa.fern", "ssa_arm64.fern", "ssa_x86.fern", "ssa_wasm.fern", "watbin.fern", "constfold.fern", "fern.fern", "wasm_run.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	wrun := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	native := arm64NativeSrc(t)

	cases := []struct{ name, src string }{
		{"exit42", `function main(): i32 { return 42; }`},
		{"arith", `function main(): i32 { var x = 6; var y = 7; return x * y; }`},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			asmPath := filepath.Join(dir, c.name+".s")
			if out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", asmPath, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("fern -target arm64-darwin (emit .s) failed: %v\n%s", err, out)
			}
			asmText, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatalf("read .s: %v", err)
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
			sb.WriteString("    if (p.unknown.len() > 0) { write(\"UNKNOWN:\" + p.unknown.join(\",\")); return 0; }\n")
			sb.WriteString("    var pa: Arm64Asm = p.asm;\n")
			sb.WriteString("    var tv: i64 = macho_text_vaddr(pa.code.len(), p.data.len());\n")
			sb.WriteString("    var dv: i64 = macho_data_vaddr(pa.code.len(), p.data.len());\n")
			sb.WriteString("    p = arm64_gas_link(p, tv, dv);\n")
			sb.WriteString("    var pa2: Arm64Asm = p.asm;\n")
			sb.WriteString("    var bin: i32[] = macho_static_executable(pa2.code, p.data, \"fern\");\n")
			sb.WriteString("    write(string_from_bytes(bin));\n    return 0;\n}\n")

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
