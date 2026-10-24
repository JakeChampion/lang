package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStage2Compiler(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	entry := "import \"./lexer\";\n" +
		"import \"./parser\";\n" +
		"import \"./asm_ir\";\n" +
		"import \"./semlower\";\n" +
		"function main(): i32 {\n" +
		"    let src: string = \"\";\n" +
		"    let r: Reader = stdin();\n" +
		"    while (true) {\n" +
		"        match (r.read_chunk(65536)) {\n" +
		"            Ok(chunk) => { if (chunk.len() == 0) { break; } src = src + chunk; },\n" +
		"            Err(_) => { break; },\n" +
		"        }\n" +
		"    }\n" +
		"    let d: semlower.Driven = semlower.driven(parser.parse_module(lexer.tokenize(src)), \"x86-64-linux\");\n" +
		"    print(asm_ir.emit_module_or_error_sub(d.full, d.sub, 0 as usize));\n" +
		"    return 0;\n" +
		"}\n"
	files := map[string]string{"main.fern": entry}
	compilerAsm, dir := compileFilesModload(t, runner, driverBin, files)
	if len(compilerAsm) == 0 {
		t.Fatal("stage 1: produced 0 bytes for the self-hosted compiler")
	}
	compilerBin := buildBin(t, gcc, dir, "fernc", compilerAsm)
	t.Logf("stage 1: self-hosted compiler built (%d bytes asm)", len(compilerAsm))

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 9; }", 9},
		{"precedence", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"parens", "function main(): i32 { return (2 + 3) * 4; }", 20},
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(20, 22); }", 42},
		{"if", "function main(): i32 { let x: i32 = 5; if (x > 3) { return 1; } return 0; }", 1},
		{"while", "function main(): i32 { let i: i32 = 0; let s: i32 = 0; while (i < 5) { s = s + i; i = i + 1; } return s; }", 10},
		{"recursion", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"string_len", "function main(): i32 { let s: string = \"hello\"; return s.len(); }", 5},
		{"array_index", "function main(): i32 { let a: i32[] = [10, 20, 30]; return a[2]; }", 30},
		{"struct_field", "function main(): i32 { let p: P = P { x: 3, y: 4 }; return p.x + p.y; } struct P { x: i32, y: i32 }", 7},
		{"method_reads_fields", "struct Pt { x: i32, y: i32 } function (p: Pt) sum(): i32 { return p.x + p.y; } function main(): i32 { let p: Pt = Pt { x: 30, y: 12 }; return p.sum(); }", 42},
		{"union_match", "struct A { v: i32 } struct B { v: i32 } type U = A | B; function f(u: U): i32 { match (u) { A(a) => { return a.v; }, B(b) => { return b.v + 100; } } return 0 - 1; } function main(): i32 { let u: U = B { v: 5 }; return f(u); }", 105},
		{"string_concat", "function main(): i32 { let s: string = \"ab\"; let t: string = s + \"cd\"; return t.len(); }", 4},
		{"string_slice", "function main(): i32 { let s: string = \"abcde\"; return slice_unchecked(s, 1, 4).len(); }", 3},
		{"and_or", "function main(): i32 { let x: i32 = 3; if (x > 1 && x < 5) { return 1; } return 0; }", 1},
		{"break_continue", "function main(): i32 { let i: i32 = 0; let c: i32 = 0; while (i < 10) { i = i + 1; if (i == 3) { continue; } if (i == 7) { break; } c = c + 1; } return c; }", 5},
		{"else_if_chain", "function main(): i32 { let x: i32 = 2; if (x == 1) { return 10; } else if (x == 2) { return 20; } else { return 30; } }", 20},
		{"for_in", "function main(): i32 { let a: i32[] = [1, 2, 3, 4]; let s: i32 = 0; for x in a { s = s + x; } return s; }", 10},
		{"struct_param", "struct Pt { x: i32, y: i32 } function dist(p: Pt): i32 { return p.x + p.y; } function main(): i32 { let p: Pt = Pt { x: 15, y: 27 }; return dist(p); }", 42},
		{"array_of_structs", "struct C { n: i32 } function main(): i32 { let arr: C[] = []; arr = arr.append(C { n: 5 }); arr = arr.append(C { n: 9 }); return arr[1].n; }", 9},
		{"nested_match", "type T = A | B; struct A { v: i32 } struct B { v: i32 } function k(t: T): i32 { match (t) { A(a) => { match (a.v) { _ => { return a.v * 2; } } }, B(b) => { return b.v; } } return 0; } function main(): i32 { let t: T = A { v: 21 }; return k(t); }", 42},
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
