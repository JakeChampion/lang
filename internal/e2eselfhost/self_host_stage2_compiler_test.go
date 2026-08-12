package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
	gcc, runner, driverBin := buildModloadDriverX86(t)

	// stage 1: a stdin-driven self-hosted compiler — its entry reads a
	// program via a read_line loop, lexes → parses → emits — compiled from
	// FILES by the file-based asm driver (no ///MODULE bundle).
	entry := "import \"./lexer\";\n" +
		"import \"./parser\";\n" +
		"import \"./asm_ir\";\n" +
		"function main(): i32 {\n" +
		"    var src: string = \"\";\n" +
		"    while (true) {\n" +
		// read_line() returns Option[string] (#4369): Some(line INCLUDING the
		// trailing '\n') / None at EOF. Accumulate each already-newline-terminated
		// line until EOF; a blank line is Some("\n"), not None.
		"        match (read_line()) {\n" +
		"            Some(line) => { src = src + line; },\n" +
		"            None => { break; },\n" +
		"        }\n" +
		"    }\n" +
		"    print(asm_ir.emit_module_or_error(parser.parse_module(lexer.tokenize(src))));\n" +
		"    return 0;\n" +
		"}\n"
	files := map[string]string{"main.fern": entry}
	for _, m := range []string{"util", "astwalk", "asmcore", "lexer", "parser", "ir", "irlower", "irverify", "irverifystack", "irverifygate", "asm_ir"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", m+".fern"))
		if err != nil {
			t.Fatalf("read %s.fern: %v", m, err)
		}
		files[m+".fern"] = string(src)
	}
	compilerAsm, dir := compileFilesModload(t, runner, driverBin, files)
	if len(compilerAsm) == 0 {
		t.Fatal("stage 1: produced 0 bytes for the self-hosted compiler")
	}
	compilerBin := buildBin(t, gcc, dir, "fernc", compilerAsm)
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
		{"string_len", "function main(): i32 { var s: string = \"hello\"; return s.len(); }", 5},
		{"array_index", "function main(): i32 { var a: i32[] = [10, 20, 30]; return a[2]; }", 30},
		// Struct field access, methods that read fields, and union
		// match — the features the compiler's OWN source is built
		// from. These exercised the method-result / struct-field type
		// inference path in asm.fern.
		{"struct_field", "function main(): i32 { var p: P = P { x: 3, y: 4 }; return p.x + p.y; } struct P { x: i32, y: i32 }", 7},
		{"method_reads_fields", "struct Pt { x: i32, y: i32 } function (p: Pt) sum(): i32 { return p.x + p.y; } function main(): i32 { var p: Pt = Pt { x: 30, y: 12 }; return p.sum(); }", 42},
		{"union_match", "struct A { v: i32 } struct B { v: i32 } type U = A | B; function f(u: U): i32 { match (u) { A(a) => { return a.v; }, B(b) => { return b.v + 100; } } return 0 - 1; } function main(): i32 { var u: U = B { v: 5 }; return f(u); }", 105},
		// More of the compiler-shaped feature set the self-hosted
		// compiler must lower to eventually compile its own source.
		{"string_concat", "function main(): i32 { var s: string = \"ab\"; var t: string = s + \"cd\"; return t.len(); }", 4},
		{"string_slice", "function main(): i32 { var s: string = \"abcde\"; return s[1:4].len(); }", 3},
		{"and_or", "function main(): i32 { var x: i32 = 3; if (x > 1 && x < 5) { return 1; } return 0; }", 1},
		{"break_continue", "function main(): i32 { var i: i32 = 0; var c: i32 = 0; while (i < 10) { i = i + 1; if (i == 3) { continue; } if (i == 7) { break; } c = c + 1; } return c; }", 5},
		{"else_if_chain", "function main(): i32 { var x: i32 = 2; if (x == 1) { return 10; } else if (x == 2) { return 20; } else { return 30; } }", 20},
		{"for_in", "function main(): i32 { var a: i32[] = [1, 2, 3, 4]; var s: i32 = 0; for x in a { s = s + x; } return s; }", 10},
		{"struct_param", "struct Pt { x: i32, y: i32 } function dist(p: Pt): i32 { return p.x + p.y; } function main(): i32 { var p: Pt = Pt { x: 15, y: 27 }; return dist(p); }", 42},
		{"array_of_structs", "struct C { n: i32 } function main(): i32 { var arr: C[] = []; arr = arr.append(C { n: 5 }); arr = arr.append(C { n: 9 }); return arr[1].n; }", 9},
		{"nested_match", "type T = A | B; struct A { v: i32 } struct B { v: i32 } function k(t: T): i32 { match (t) { A(a) => { match (a.v) { _ => { return a.v * 2; } } }, B(b) => { return b.v; } } return 0; } function main(): i32 { var t: T = A { v: 21 }; return k(t); }", 42},
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
