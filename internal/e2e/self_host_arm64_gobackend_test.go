package e2e

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	x8664 "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostArm64NativeViaGoBackend assembles the self-host asm_arm64
// emitter's real darwin output through arm64_native.fern compiled by the
// *Go x86-64 reference backend* — the backend the unified `fern` CLI is
// built with (TestSelfHostArm64DarwinAssemblesRealRuntime proves the same
// via the wasm backend). This is the backend that will run arm64_native
// once `-target arm64-darwin` flips to the in-Fern path, and it exposed a
// real codegen bug: the Go x86 backend miscompiles a struct spread-update
// of a function *parameter* (segfault). arm64_native binds a local copy in
// every such function (see the "local copy" notes); without those this test
// segfaults. Here we assert the in-Fern assembler + Mach-O writer produce a
// valid arm64 MH_EXECUTE for real compiler output under the Go x86 backend.
//
// The cases deliberately span the instruction forms a real compiler emits:
// string concat (the signed-offset `stp/ldp [sp, #off]` large-frame forms),
// i64 math (`sxtw`), and bitwise ops (the register `lsl/lsr/asr` shifts).
// Those forms were originally unhandled by arm64_native — the `ldp` offset
// form indexed ops[3] out of range (a bounds abort that *looked* like a Go
// x86 backend miscompile but was a real assembler gap), `stp [sp,#off]` was
// silently mis-encoded as pre-index, and the register shifts / `sxtw` were
// missing — see SELF-HOST-REMAINING-PLAN.md (slice 3p) and the byte-pinned
// TestSelfHostArm64OffsetPairGas.
func TestSelfHostArm64NativeViaGoBackend(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("native x86-64 run required")
	}

	// Stage the self-host project + fern.fern (still the .s emitter on main)
	// so we can produce the real darwin assembly text.
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asmcore.fern", "flatten.fern", "asm_arm64.fern", "wasm.fern", "checker.fern", "interp.fern", "printer.fern", "ssa.fern", "ssa_arm64.fern", "ssa_x86.fern", "ssa_wasm.fern", "watbin.fern", "constfold.fern", "fern.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	native := string(mustRead(t, "../../examples/self_host/arm64_native.fern"))

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
			asmPath := filepath.Join(dir, c.name+".s")
			if out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", asmPath, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("emit .s failed: %v\n%s", err, out)
			}
			asmText, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatalf("read .s: %v", err)
			}

			// A driver that assembles the real darwin asm via arm64_native
			// and writes the Mach-O — compiled by the Go x86-64 backend.
			edir := t.TempDir()
			if err := os.WriteFile(filepath.Join(edir, "arm64_native.fern"), []byte(native), 0o644); err != nil {
				t.Fatalf("write arm64_native: %v", err)
			}
			var sb strings.Builder
			sb.WriteString("import \"./arm64_native\";\n")
			sb.WriteString("function to_u8(b: i32[]): u8[] { var o: u8[] = []; var i: i32 = 0; while (i < b.len()) { o = o.append(b[i] as u8); i = i + 1; } return o; }\n")
			sb.WriteString("function main(): i32 {\n    var asm: string = \"\";\n")
			for _, ln := range strings.Split(string(asmText), "\n") {
				sb.WriteString("    asm = asm + \"" + fernEscapeAsmLine(ln) + "\\n\";\n")
			}
			sb.WriteString("    var p: arm64_native.Arm64GasProg = arm64_native.arm64_gas_program(asm);\n")
			sb.WriteString("    if (p.unknown.len() > 0) { write(\"UNKNOWN:\" + p.unknown[0]); return 0; }\n")
			sb.WriteString("    var pa: arm64_native.Arm64Asm = p.asm;\n")
			sb.WriteString("    var tv: i64 = arm64_native.macho_text_vaddr(pa.code.len(), p.data.len());\n")
			sb.WriteString("    var dv: i64 = arm64_native.macho_data_vaddr(pa.code.len(), p.data.len());\n")
			sb.WriteString("    p = arm64_native.arm64_gas_link(p, tv, dv);\n")
			sb.WriteString("    var pa2: arm64_native.Arm64Asm = p.asm;\n")
			sb.WriteString("    var bin: i32[] = arm64_native.macho_static_executable(pa2.code, p.data, \"fern\");\n")
			sb.WriteString("    write(string_from_bytes(to_u8(bin)));\n    return 0;\n}\n")
			entryPath := filepath.Join(edir, "drv.fern")
			if err := os.WriteFile(entryPath, []byte(sb.String()), 0o644); err != nil {
				t.Fatalf("write driver: %v", err)
			}

			prog, _, err := modload.Load(entryPath)
			if err != nil {
				t.Fatalf("modload driver: %v", err)
			}
			if err := constfold.Fold(prog); err != nil {
				t.Fatalf("constfold: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			asm, err := x8664.Emit(prog, info)
			if err != nil {
				t.Fatalf("x86 emit: %v", err)
			}
			drvBin := buildBin(t, gcc, edir, "drv", asm)
			bin, err := exec.Command(drvBin).Output()
			if err != nil {
				t.Fatalf("driver run (Go x86 backend): %v", err)
			}
			if bytes.HasPrefix(bin, []byte("UNKNOWN:")) {
				t.Fatalf("arm64_native reported an unknown instruction: %s", bin)
			}
			f, err := macho.NewFile(bytes.NewReader(bin))
			if err != nil {
				t.Fatalf("assembled output is not a parseable Mach-O: %v (len=%d)", err, len(bin))
			}
			if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
				t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
			}
		})
	}
}
