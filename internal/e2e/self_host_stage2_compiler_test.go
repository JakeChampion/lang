package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostStage2Compiler builds a reusable, stdin-driven
// self-hosted compiler and exercises it across real language features.
//
// Stage 1 bundles lexer.fern + parser.fern + asm.fern with an entry
// that reads a program from stdin (a read_line loop), lexes → parses →
// emits, and prints the asm. flatten.bundle merges them and
// asm.emit_module lowers the whole thing; gcc links it into ONE
// self-hosted compiler binary — effectively a Fern-authored `fern`.
//
// Stage 2 then feeds that single compiler a table of programs over
// stdin and assembles + runs each emitted result, asserting the exit
// code. This proves the self-hosted compiler isn't a one-trick
// `return 7`: it correctly lowers arithmetic precedence, function
// calls, conditionals, loops, and recursion.
func TestSelfHostStage2Compiler(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "flatten.fern", "asm.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// stage 0: build bundle_run.
	prog, _, err := modload.Load(filepath.Join(dir, "bundle_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	// stage 1: bundle into a stdin-driven self-hosted compiler.
	lexerSrc, _ := os.ReadFile("../../examples/self_host/lexer.fern")
	parserSrc, _ := os.ReadFile("../../examples/self_host/parser.fern")
	asmSrc, _ := os.ReadFile("../../examples/self_host/asm.fern")
	entry := "import \"./lexer\";\n" +
		"import \"./parser\";\n" +
		"import \"./asm\";\n" +
		"function main(): i32 {\n" +
		"    var src: string = \"\";\n" +
		"    while (true) {\n" +
		"        var line: string = read_line();\n" +
		"        if (line.len() == 0) { break; }\n" +
		"        src = src + line;\n" +
		"        src = src + \"\\n\";\n" +
		"    }\n" +
		"    print(asm.emit_module(parser.parse_module(lexer.tokenize(src))));\n" +
		"    return 0;\n" +
		"}\n"

	var bundle bytes.Buffer
	bundle.WriteString("///MODULE lexer\n")
	bundle.Write(lexerSrc)
	bundle.WriteString("\n///MODULE parser\n")
	bundle.Write(parserSrc)
	bundle.WriteString("\n///MODULE asm\n")
	bundle.Write(asmSrc)
	bundle.WriteString("\n///MODULE main\n")
	bundle.WriteString(entry)

	compilerAsm := runCapture(t, gcc, runner, driverBin, bundle.Bytes())
	if len(compilerAsm) == 0 {
		t.Fatal("stage 1: bundler produced 0 bytes for the self-hosted compiler")
	}
	compilerAsmPath := filepath.Join(dir, "fernc.s")
	compilerBin := filepath.Join(dir, "fernc")
	if err := os.WriteFile(compilerAsmPath, compilerAsm, 0o644); err != nil {
		t.Fatalf("write compiler asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", compilerAsmPath, "-o", compilerBin).CombinedOutput(); err != nil {
		t.Fatalf("stage 1: gcc on self-hosted compiler: %v\n%s", err, out)
	}
	t.Logf("stage 1: self-hosted compiler built (%d bytes asm)", len(compilerAsm))

	// stage 2: compile a table of programs with the self-hosted
	// compiler; each emitted binary's exit code must match. Programs
	// are single-line (the read_line loop stops at the first blank
	// line / EOF).
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 9; }", 9},
		{"precedence", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"parens", "function main(): i32 { return (2 + 3) * 4; }", 20},
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(20, 22); }", 42},
		{"if", "function main(): i32 { var x: i32 = 5; if (x > 3) { return 1; } return 0; }", 1},
		{"while", "function main(): i32 { var i: i32 = 0; var s: i32 = 0; while (i < 5) { s = s + i; i = i + 1; } return s; }", 10},
		{"recursion", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			progAsm := runCapture(t, gcc, runner, compilerBin, []byte(c.src+"\n"))
			if len(progAsm) == 0 {
				t.Fatalf("self-hosted compiler emitted 0 bytes for %q", c.src)
			}
			progAsmPath := filepath.Join(dir, c.name+".s")
			progBin := filepath.Join(dir, c.name+".bin")
			if err := os.WriteFile(progAsmPath, progAsm, 0o644); err != nil {
				t.Fatalf("write %s asm: %v", c.name, err)
			}
			if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", progAsmPath, "-o", progBin).CombinedOutput(); err != nil {
				t.Fatalf("gcc on emitted %s: %v\n%s", c.name, err, out)
			}
			var pcmd *exec.Cmd
			if len(runner) == 0 {
				pcmd = exec.Command(progBin)
			} else {
				pcmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_, _ = pcmd.CombinedOutput()
			if code := pcmd.ProcessState.ExitCode(); code != c.want {
				t.Errorf("self-hosted-compiled %q exited %d, want %d", c.src, code, c.want)
			}
		})
	}
}
