package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostCLIX86_64 exercises examples/self_host/fern.fern — the
// unified self-hosted `fern` CLI driver. Unlike the single-mode
// `*_run.fern` shims (asm_load_run = emit, checker_run = check), this is
// ONE binary that parses argv flags and dispatches mode + output. The
// test builds it with the Go backend, then drives each mode:
//
//   - default            emit x86-64 asm to stdout; assemble + run.
//   - `-o OUT`           emit to a file; assemble that file + run.
//   - `-check` (ok)      well-typed program exits 0.
//   - `-check` (bad)     ill-typed program exits 1.
//   - usage / errors     no-arg → 2, unknown flag → 2, missing file → 1.
//
// It is the first parity slice toward retiring cmd/fern: the
// flag-dispatch seam the -target / -interp / -fmt modes plug into next.
func TestSelfHostCLIX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// The driver takes host filesystem paths as argv; a qemu runner
		// wouldn't see the same paths. Native-only, like the file driver.
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t) // lexer.fern, parser.fern, asm.fern
	for _, name := range []string{"flatten.fern", "asm_arm64.fern", "wasm.fern", "checker.fern", "interp.fern", "printer.fern", "ssa.fern", "ssa_x86.fern", "ssa_arm64.fern", "fern.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Build the CLI driver with the Go backend.
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	// runDriver runs the CLI with args and returns (stdout, exitcode).
	runDriver := func(t *testing.T, args ...string) ([]byte, int) {
		t.Helper()
		cmd := exec.Command(fernBin, args...)
		out, _ := cmd.Output()
		return out, cmd.ProcessState.ExitCode()
	}

	t.Run("emit-stdout", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ret42.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		asm, code := runDriver(t, srcPath)
		if code != 0 {
			t.Fatalf("emit exited %d, want 0", code)
		}
		if len(asm) == 0 {
			t.Fatal("emit produced 0 bytes of asm")
		}
		progBin := buildBin(t, gcc, dir, "ret42", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("emitted program exited %d, want 42", c)
		}
	})

	t.Run("emit-to-file", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ret7.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { var x = 3; var y = 4; return x + y; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		outPath := filepath.Join(dir, "ret7.s")
		stdout, code := runDriver(t, "-o", outPath, srcPath)
		if code != 0 {
			t.Fatalf("`-o` emit exited %d, want 0", code)
		}
		if len(stdout) != 0 {
			t.Errorf("`-o` emit wrote %d bytes to stdout, want 0 (asm goes to the file)", len(stdout))
		}
		asm, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read -o output: %v", err)
		}
		if len(asm) == 0 {
			t.Fatal("`-o` output file is empty")
		}
		progBin := buildBin(t, gcc, dir, "ret7", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 7 {
			t.Errorf("file-emitted program exited %d, want 7", c)
		}
	})

	t.Run("emit-ssa", func(t *testing.T) {
		// -ssa routes an in-subset program through AST → SSA → optimise →
		// regalloc → emit. Assemble + run; exit code is the program's value.
		srcPath := filepath.Join(dir, "ssa_prog.fern")
		src := "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } " +
			"function main(): i32 { var s = 0; var i = 0; while (i < 10) { s = s + fib(i); i = i + 1; } return s; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		asm, code := runDriver(t, "-ssa", srcPath)
		if code != 0 {
			t.Fatalf("-ssa emit exited %d, want 0", code)
		}
		progBin := buildBin(t, gcc, dir, "ssa_prog", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 88 {
			t.Errorf("-ssa emitted program exited %d, want 88 (sum of fib(0..9))", c)
		}
	})

	t.Run("emit-ssa-array", func(t *testing.T) {
		// An array program is now in the SSA subset on x86-64: -ssa compiles
		// it through the heap-aware backend (not a fallback). Run it.
		srcPath := filepath.Join(dir, "ssa_arr.fern")
		src := "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		ssaAsm, code := runDriver(t, "-ssa", srcPath)
		if code != 0 {
			t.Fatalf("-ssa emit exited %d, want 0", code)
		}
		astAsm, _ := runDriver(t, srcPath)
		if string(ssaAsm) == string(astAsm) {
			t.Error("-ssa fell back to AST for an array program (expected the SSA heap backend)")
		}
		progBin := buildBin(t, gcc, dir, "ssa_arr", string(ssaAsm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 75 {
			t.Errorf("-ssa array program exited %d, want 75", c)
		}
	})

	t.Run("emit-ssa-helpers", func(t *testing.T) {
		// A program using push + slice pulls in the injected runtime helpers
		// (__ssa_arr_push / __ssa_arr_slice). try_ssa must inject those helper
		// bodies AND admit their names to the known set; otherwise calls_all_known
		// rejects the call and the whole program falls back to the AST emitter.
		// Asserting the -ssa output differs from AST proves the SSA path (with
		// injected helpers) was taken.
		srcPath := filepath.Join(dir, "ssa_helpers.fern")
		src := "function main(): i32 { var a = [1, 2]; a = a.push(3); var b = a[1:3]; return b[0] + b[1] + b.len() + a.len(); }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		ssaAsm, code := runDriver(t, "-ssa", srcPath)
		if code != 0 {
			t.Fatalf("-ssa emit exited %d, want 0", code)
		}
		astAsm, _ := runDriver(t, srcPath)
		if string(ssaAsm) == string(astAsm) {
			t.Error("-ssa fell back to AST for a push/slice program (runtime helpers not injected)")
		}
		progBin := buildBin(t, gcc, dir, "ssa_helpers", string(ssaAsm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 10 {
			t.Errorf("-ssa push/slice program exited %d, want 10", c)
		}
	})

	t.Run("ssa-fallback", func(t *testing.T) {
		// A program outside the SSA subset (a float local) must fall back to
		// the AST emitter: -ssa output is byte-identical to the default.
		srcPath := filepath.Join(dir, "fallback.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { var x = 1.5; return 5; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		withSSA, code1 := runDriver(t, "-ssa", srcPath)
		astOnly, code2 := runDriver(t, srcPath)
		if code1 != 0 || code2 != 0 {
			t.Fatalf("emit exited %d / %d, want 0", code1, code2)
		}
		if string(withSSA) != string(astOnly) {
			t.Errorf("-ssa did not fall back cleanly for an out-of-subset program (output differs from AST)")
		}
	})

	t.Run("check-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ok.fern")
		if err := os.WriteFile(srcPath, []byte("function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-check", srcPath)
		if code != 0 {
			t.Errorf("-check on well-typed program exited %d, want 0", code)
		}
	})

	t.Run("check-bad", func(t *testing.T) {
		// Arity mismatch — the self-host checker rejects it (mirrors
		// checker.fern's own mt6 self-test).
		srcPath := filepath.Join(dir, "bad.fern")
		if err := os.WriteFile(srcPath, []byte("function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(1); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-check", srcPath)
		if code != 1 {
			t.Errorf("-check on ill-typed program exited %d, want 1", code)
		}
		// The self-host checker surfaces the stable diagnostic code (the
		// same E0XX the Go checker emits) on stderr — here E004 for the
		// arity mismatch.
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if !strings.Contains(string(combined), "error[E004]") {
			t.Errorf("-check diagnostics = %q, want it to contain error[E004]", combined)
		}
	})

	t.Run("check-position", func(t *testing.T) {
		// A diagnostic emitted from an AST node that carries a source
		// position renders `line:col: error[E0XX]: …`, matching the Go
		// checker's format. An enum redeclared on line 2 → `2:1`.
		srcPath := filepath.Join(dir, "enum_redecl.fern")
		if err := os.WriteFile(srcPath, []byte("enum Opt { A, B }\nenum Opt { C, D }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if !strings.Contains(string(combined), "2:1: error[E006]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"2:1: error[E006]\"", combined)
		}
	})

	t.Run("check-position-struct", func(t *testing.T) {
		// E007 (duplicate field) is reported at the struct decl position.
		srcPath := filepath.Join(dir, "dup_field.fern")
		if err := os.WriteFile(srcPath, []byte("struct P { x: i32, x: i32 }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if !strings.Contains(string(combined), "1:1: error[E007]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"1:1: error[E007]\"", combined)
		}
	})

	t.Run("check-position-func", func(t *testing.T) {
		// E018 (dup param) reports at the function decl; E006 (redeclared)
		// at the redeclaration site.
		dupParam := filepath.Join(dir, "dup_param.fern")
		if err := os.WriteFile(dupParam, []byte("function f(a: i32, a: i32): i32 { return a; }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out1, _ := exec.Command(fernBin, "-check", dupParam).CombinedOutput()
		if !strings.Contains(string(out1), "1:1: error[E018]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"1:1: error[E018]\"", out1)
		}
		redecl := filepath.Join(dir, "func_redecl.fern")
		if err := os.WriteFile(redecl, []byte("function f(): i32 { return 1; }\nfunction f(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out2, _ := exec.Command(fernBin, "-check", redecl).CombinedOutput()
		if !strings.Contains(string(out2), "2:1: error[E006]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"2:1: error[E006]\"", out2)
		}
	})

	t.Run("check-position-var", func(t *testing.T) {
		// Statement-level codes are reported at the `var` keyword: E013
		// (dup var) at the redeclaration, E003 (annotated-var mismatch) and
		// E020 (empty array, no annotation) at the var.
		for _, c := range []struct{ name, src, want string }{
			{"dup_var", "function main(): i32 { var x: i32 = 1; var x: i32 = 2; return x; }\n", "1:40: error[E013]"},
			{"var_mismatch", "function main(): i32 { var x: i32 = \"no\"; return x; }\n", "1:24: error[E003]"},
			{"empty_array", "function main(): i32 { var z = []; return 0; }\n", "1:24: error[E020]"},
			{"assign_mismatch", "function main(): i32 { var x: i32 = 1; x = \"no\"; return x; }\n", "1:42: error[E003]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-return", func(t *testing.T) {
		// E002 (return-type mismatch) and E012 (return without value) are
		// reported at the `return` keyword.
		for _, c := range []struct{ name, src, want string }{
			{"ret_mismatch", "function main(): i32 { var s: string = \"x\"; return s; }\n", "1:45: error[E002]"},
			{"ret_no_value", "function f(): i32 { return; }\nfunction main(): i32 { return 0; }\n", "1:21: error[E012]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-method-argtype", func(t *testing.T) {
		// E038 for a primitive method-argument type mismatch is reported
		// at the offending argument.
		for _, c := range []struct{ name, src, want string }{
			{"string_for_i32", "struct P { x: i32 }\nfunction (p: P) add(a: i32): i32 { return p.x + a; }\nfunction main(): i32 { var p: P = P { x: 1 }; var s: string = \"n\"; return p.add(s); }\n", "3:81: error[E038]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-method-arity", func(t *testing.T) {
		// E004 for a method call with the wrong argument count is reported
		// at the call's opening paren.
		for _, c := range []struct{ name, src, want string }{
			{"method_too_few", "struct P { x: i32 }\nfunction (p: P) add(a: i32, b: i32): i32 { return p.x + a + b; }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.add(5); }\n", "3:59: error[E004]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-match-nonenum", func(t *testing.T) {
		// E035 (variant pattern on a non-enum scrutinee) is reported at
		// the offending arm.
		for _, c := range []struct{ name, src, want string }{
			{"variant_on_i32", "enum E { A, B }\nfunction main(): i32 { var n: i32 = 5; match (n) { A => { return 1; }, _ => { return 0; } } }\n", "2:52: error[E035]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-array-elem", func(t *testing.T) {
		// E034 (heterogeneous array element) is reported at the offending
		// element's own position.
		for _, c := range []struct{ name, src, want string }{
			{"string_in_i32", "function main(): i32 { var a = [1, \"x\", 3]; return 0; }\n", "1:36: error[E034]"},
			{"i32_in_string", "function main(): i32 { var a = [\"a\", 1]; return 0; }\n", "1:38: error[E034]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-type-arity", func(t *testing.T) {
		// E019 (generic-struct type-arg count mismatch) is reported at
		// the struct's declaration, not the use site.
		for _, c := range []struct{ name, src, want string }{
			{"too_many_args", "struct Box[T] { v: T }\nfunction f(b: Box[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", "1:1: error[E019]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-match-arm", func(t *testing.T) {
		// E026 (non-final wildcard) and E028 (duplicate variant) are
		// reported at the offending arm (its pattern's first token).
		for _, c := range []struct{ name, src, want string }{
			{"wildcard_not_last", "enum E { A, B }\nfunction main(): i32 { var e: E = E.A; match (e) { _ => { return 0; }, A => { return 1; } } }\n", "2:52: error[E026]"},
			{"dup_variant", "enum E { A, B }\nfunction main(): i32 { var e: E = E.A; match (e) { A => { return 0; }, A => { return 1; }, B => { return 2; } } }\n", "2:72: error[E028]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-break-continue", func(t *testing.T) {
		// E011 (break / continue outside a loop) is reported at the
		// keyword.
		for _, c := range []struct{ name, src, want string }{
			{"break", "function main(): i32 { break; return 0; }\n", "1:24: error[E011]"},
			{"continue", "function main(): i32 { continue; return 0; }\n", "1:24: error[E011]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-cond-slice", func(t *testing.T) {
		// E008 (if/while condition) and E037 (slice bound) are reported
		// at the offending expression's own position.
		for _, c := range []struct{ name, src, want string }{
			{"if_cond", "function main(): i32 { if (5) { return 1; } return 0; }\n", "1:28: error[E008]"},
			{"while_cond", "function main(): i32 { while (5) { return 1; } return 0; }\n", "1:31: error[E008]"},
			{"slice_low", "function main(): i32 { var a = [1,2,3]; var s: string = \"x\"; var b = a[s:2]; return 0; }\n", "1:72: error[E037]"},
			{"slice_high", "function main(): i32 { var a = [1,2,3]; var s: string = \"x\"; var b = a[0:s]; return 0; }\n", "1:74: error[E037]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-field", func(t *testing.T) {
		// E043 (no such struct field) and E046 (bad tuple index) are
		// reported at the field-access dot.
		for _, c := range []struct{ name, src, want string }{
			{"no_field", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.y; }\n", "2:55: error[E043]"},
			{"bad_tuple_idx", "function main(): i32 { var t = (1, 2); return t.foo; }\n", "1:48: error[E046]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-operator", func(t *testing.T) {
		// E009 (boolean/numeric operand) and E041 (compare mismatch) are
		// reported at the operator token: the binary operator, or the
		// unary `!`.
		for _, c := range []struct{ name, src, want string }{
			{"and_nonbool", "function main(): i32 { var b: bool = true; var x: bool = b && 5; return 0; }\n", "1:60: error[E009]"},
			{"not_nonbool", "function main(): i32 { var x: bool = !5; return 0; }\n", "1:38: error[E009]"},
			{"compare_mismatch", "function main(): i32 { var t = (1 == \"x\"); return 0; }\n", "1:35: error[E041]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-ident", func(t *testing.T) {
		// E036 (ambiguous unqualified variant) and the ident-argument
		// case of E038 are reported at the identifier's own position.
		for _, c := range []struct{ name, src, want string }{
			{"ambiguous_variant", "enum A { Foo, Bar }\nenum B { Foo, Baz }\nfunction main(): i32 { var x = Foo; return 0; }\n", "3:32: error[E036]"},
			{"arg_ident", "function f(a: string): i32 { return 0; }\nfunction main(): i32 { var n: i32 = 5; return f(n); }\n", "2:49: error[E038]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-number", func(t *testing.T) {
		// E047 (literal out of range) and the number-argument case of
		// E038 (argument type mismatch) are reported at the numeric
		// literal's own position.
		for _, c := range []struct{ name, src, want string }{
			{"lit_overflow", "function main(): i32 { var x: i32 = 9999999999; return x; }\n", "1:37: error[E047]"},
			{"arg_number", "function f(a: string): i32 { return 0; }\nfunction main(): i32 { return f(5); }\n", "2:33: error[E038]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-call", func(t *testing.T) {
		// E004 (free-call arity mismatch) is reported at the call's
		// opening paren, matching the Go parser's Call{P: open.Pos}.
		for _, c := range []struct{ name, src, want string }{
			{"call_arity", "function f(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return f(1); }\n", "2:32: error[E004]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("check-position-structlit", func(t *testing.T) {
		// E005 (struct literal missing field) is reported at the
		// struct-literal type name.
		for _, c := range []struct{ name, src, want string }{
			{"missing_field", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.x; }\n", "2:35: error[E005]"},
		} {
			sp := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			out, _ := exec.Command(fernBin, "-check", sp).CombinedOutput()
			if !strings.Contains(string(out), c.want) {
				t.Errorf("%s: -check diagnostics = %q, want %q", c.name, out, c.want)
			}
		}
	})

	t.Run("interp", func(t *testing.T) {
		// -interp evaluates via the tree-walker; the program's i32
		// result becomes the exit code (mirrors interp_run.fern).
		srcPath := filepath.Join(dir, "interp_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { var x = 5; var y = 8; return x * y - 1; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-interp", srcPath)
		if code != 39 {
			t.Errorf("-interp program exited %d, want 39", code)
		}
	})

	t.Run("fmt", func(t *testing.T) {
		// -fmt formats the entry file to stdout. We assert it produces
		// non-empty output and is IDEMPOTENT (formatting the formatted
		// output is a fixed point) — validating the formatter is wired
		// + stable without pinning its exact style.
		srcPath := filepath.Join(dir, "messy.fern")
		messy := "function   main( ):i32{var x=1;var y=2;return x+y;}\n"
		if err := os.WriteFile(srcPath, []byte(messy), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out1, code := runDriver(t, "-fmt", srcPath)
		if code != 0 {
			t.Fatalf("-fmt exited %d, want 0", code)
		}
		if len(out1) == 0 {
			t.Fatal("-fmt produced empty output")
		}
		// Re-format the formatted output; must be a fixed point.
		src2Path := filepath.Join(dir, "formatted.fern")
		if err := os.WriteFile(src2Path, out1, 0o644); err != nil {
			t.Fatalf("write formatted: %v", err)
		}
		out2, code2 := runDriver(t, "-fmt", src2Path)
		if code2 != 0 {
			t.Fatalf("second -fmt exited %d, want 0", code2)
		}
		if string(out1) != string(out2) {
			t.Errorf("-fmt is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
		}
		// And the formatted output must still be well-typed.
		_, checkCode := runDriver(t, "-check", src2Path)
		if checkCode != 0 {
			t.Errorf("formatted output failed -check (exit %d)", checkCode)
		}
	})

	t.Run("emit-target-arm64", func(t *testing.T) {
		// The driver (x86-64 host binary) emits arm64 asm under
		// -target arm64; cross-assemble it and run under qemu.
		arm64gcc, qemu := arm64Tooling(t) // skips if cross toolchain absent
		srcPath := filepath.Join(dir, "arm_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		asm, code := runDriver(t, "-target", "arm64", srcPath)
		if code != 0 {
			t.Fatalf("-target arm64 emit exited %d, want 0", code)
		}
		if len(asm) == 0 {
			t.Fatal("-target arm64 produced 0 bytes of asm")
		}
		progBin := buildBinArm64(t, arm64gcc, dir, "arm_prog", string(asm))
		cmd := exec.Command(qemu, progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("arm64-emitted program exited %d, want 42", c)
		}
	})

	t.Run("emit-target-wasm", func(t *testing.T) {
		// The driver emits a WASI WAT module under -target wasm; run it
		// directly with wasmtime and check exit code + stdout.
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping -target wasm")
		}
		srcPath := filepath.Join(dir, "wasm_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { print(\"hi from wasm\"); return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm", srcPath)
		if code != 0 {
			t.Fatalf("-target wasm emit exited %d, want 0", code)
		}
		if len(wat) == 0 {
			t.Fatal("-target wasm produced 0 bytes")
		}
		watPath := filepath.Join(dir, "wasm_prog.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.Output()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("wasm-emitted program exited %d, want 42\n--- WAT ---\n%s", c, wat)
		}
		if string(out) != "hi from wasm\n" {
			t.Errorf("wasm-emitted program stdout = %q, want %q", string(out), "hi from wasm\n")
		}
	})

	t.Run("unknown-target-exits-2", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ret42.fern")
		_, code := runDriver(t, "-target", "riscv", srcPath)
		if code != 2 {
			t.Errorf("unknown -target exited %d, want 2", code)
		}
	})

	t.Run("no-arg-exits-2", func(t *testing.T) {
		_, code := runDriver(t)
		if code != 2 {
			t.Errorf("no-arg driver exited %d, want 2", code)
		}
	})

	t.Run("unknown-flag-exits-2", func(t *testing.T) {
		_, code := runDriver(t, "-nope", filepath.Join(dir, "ret42.fern"))
		if code != 2 {
			t.Errorf("unknown-flag driver exited %d, want 2", code)
		}
	})

	t.Run("missing-file-exits-1", func(t *testing.T) {
		_, code := runDriver(t, filepath.Join(dir, "does-not-exist.fern"))
		if code != 1 {
			t.Errorf("missing-file driver exited %d, want 1", code)
		}
	})
}
