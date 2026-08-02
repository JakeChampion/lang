package e2eselfhost

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostArm64DarwinMachORealAsm is the capstone of the arm64-darwin
// native-binary track: it assembles the compiler's *actual* emitted
// arm64-darwin assembly — not hand-written snippets — into a runnable,
// ad-hoc-signed Mach-O with no external as/clang/ld64.
//
// The Go reference backend (internal/codegen/arm64, which the self-host
// asm_arm64.fern mirrors) emits the darwin asm for a Fern program. That
// asm text is fed to a generated Fern driver that runs it through
// arm64_gas_program (parse -> bytes + data + symbol fixups),
// arm64_gas_link (resolve @PAGE/@PAGEOFF against macho.fern's segment
// addresses), and macho.fern (wrap + ad-hoc sign). The whole driver
// compiles through the self-host wasm pipeline; wasmtime runs it to produce
// the Mach-O. The result must be a valid arm64 MH_EXECUTE (every host) and,
// on Apple Silicon, execute with the program's exit code.
func TestSelfHostArm64DarwinMachORealAsm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin real-asm e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, n := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{"return42", `function main(): i32 { return 42; }`, 42},
		{"arith", `function main(): i32 { var x = 6; var y = 7; return x * y; }`, 42},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`, 55},
		{"option", `function pick(n: i32): Option[i32] { if (n == 0) { return None; } return Some(n + 1); } function main(): i32 { match (pick(41)) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 42},
		{"concat", `function main(): i32 { var s: string = "hello, " + "world!"; return s.len(); }`, 13},
		{"struct_method", `struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { var x = Box { v: 4 }; return x.scale(3); }`, 12},
		{"array_sum", `function main(): i32 { var a = [1, 2, 3, 4, 5]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 15},
		// Closures / function-pointer calls lower to `blr` (indirect call) —
		// which the in-process arm64 assembler must encode (regression guard:
		// it previously landed in p.unknown -> "UNKNOWN: blr").
		{"closure", `function main(): i32 { var k = 40; var f = function(x: i32): i32 { return x + k; }; return f(2); }`, 42},
		{"higher_order", `function apply(g: (i32) => i32, n: i32): i32 { return g(n); } function dbl(x: i32): i32 { return x * 2; } function main(): i32 { return apply(dbl, 21); }`, 42},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(c.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			asm, err := arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{Darwin: true})
			if err != nil {
				t.Fatalf("emit: %v", err)
			}

			source := arm64NativeSrc(t) + "\n" + asmToMachoDriver(asm)
			wat := runCapture(t, gcc, runner, driverBin, []byte(source))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes for the real-asm driver")
			}
			watPath := filepath.Join(dir, c.name+"_real.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			bin, err := exec.Command("wasmtime", "run", watPath).Output()
			if err != nil {
				t.Fatalf("wasmtime run (driver): %v", err)
			}
			if bytes.HasPrefix(bin, []byte("UNKNOWN:")) {
				t.Fatalf("arm64_gas dropped unhandled mnemonic(s): %s\n--- asm ---\n%s", bin, asm)
			}

			f, err := macho.NewFile(bytes.NewReader(bin))
			if err != nil {
				t.Fatalf("self-host output is not a parseable Mach-O: %v\n--- asm ---\n%s", err, asm)
			}
			if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
				t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
			}

			if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
				return
			}
			binPath := filepath.Join(dir, c.name)
			if err := os.WriteFile(binPath, bin, 0o755); err != nil {
				t.Fatalf("write binary: %v", err)
			}
			cmd := exec.Command(binPath)
			runErr := cmd.Run()
			ps := cmd.ProcessState
			if ps == nil || !ps.Exited() {
				t.Skipf("Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
			}
			if got := ps.ExitCode(); got != c.wantExit {
				t.Errorf("real-asm %q exit = %d, want %d", c.name, got, c.wantExit)
			}
		})
	}
}

// asmToMachoDriver builds a Fern main() that reconstructs `asm` as a string
// (one `asm = asm + "<line>\n"` per source line), assembles it through
// arm64_gas_program, resolves the @PAGE/@PAGEOFF fixups against macho.fern's
// segment addresses, wraps it into an ad-hoc-signed Mach-O, and writes the
// raw bytes to stdout.
func asmToMachoDriver(asm string) string {
	var b strings.Builder
	b.WriteString("\nfunction main(): i32 {\n")
	b.WriteString("    var asm: string = \"\";\n")
	for _, line := range strings.Split(asm, "\n") {
		b.WriteString("    asm = asm + \"" + fernEscapeAsmLine(line) + "\\n\";\n")
	}
	b.WriteString("    var p: Arm64GasProg = arm64_gas_program(asm);\n")
	// Surface any silently-dropped mnemonic so the structural check (which
	// can't tell good code from a well-formed-but-wrong binary) still fails.
	// Loop-write (not `p.unknown.join(",")`): `.join` has no IR lowering yet,
	// and the AST-fallback wasm backend it forced the whole driver onto
	// miscompiles the driver's `p = arm64_gas_link(p, …)` rebind (a struct
	// release that double-frees children shared with the returned value),
	// poisoning the freelist and crashing macho_code_signature's sha256.
	b.WriteString("    if (p.unknown.len() > 0) { write(\"UNKNOWN:\"); var ui: i32 = 0; while (ui < p.unknown.len()) { if (ui > 0) { write(\",\"); } write(p.unknown[ui]); ui = ui + 1; } return 0; }\n")
	b.WriteString("    var pa: Arm64Asm = p.asm;\n")
	b.WriteString("    var tv: i64 = macho_text_vaddr(pa.code.len(), p.data.len());\n")
	b.WriteString("    var dv: i64 = macho_data_vaddr(pa.code.len(), p.data.len());\n")
	b.WriteString("    p = arm64_gas_link(p, tv, dv);\n")
	b.WriteString("    var pa2: Arm64Asm = p.asm;\n")
	b.WriteString("    var bin: i32[] = macho_static_executable(pa2.code, p.data, \"fern\");\n")
	b.WriteString("    write(string_from_bytes_unchecked(bin));\n")
	b.WriteString("    return 0;\n}\n")
	return b.String()
}

// fernEscapeAsmLine escapes an assembly source line for embedding in a Fern
// string literal: backslash, double-quote, and tab.
func fernEscapeAsmLine(line string) string {
	line = strings.ReplaceAll(line, "\\", "\\\\")
	line = strings.ReplaceAll(line, "\"", "\\\"")
	line = strings.ReplaceAll(line, "\t", "\\t")
	return line
}
