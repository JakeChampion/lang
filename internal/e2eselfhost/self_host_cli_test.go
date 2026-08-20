package e2eselfhost

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	copySelfHostDriver(t, dir, "fern.fern")

	// Build the CLI driver with the Go backend.
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	// runDriver runs the CLI with args and returns (stdout, exitcode).
	//
	// Selects `-emit asm` when the caller names no target, because these
	// cases assert on GAS TEXT and then hand it to gcc. The default output
	// form assembles and links in-process and would hand them an ELF.
	runDriver := func(t *testing.T, args ...string) ([]byte, int) {
		t.Helper()
		if !slices.Contains(args, "-target") {
			args = append([]string{"-target", "x86-64-linux", "-emit", "asm"}, args...)
		}
		cmd := exec.Command(fernBin, args...)
		out, _ := cmd.Output()
		return out, cmd.ProcessState.ExitCode()
	}

	// `async function` end-to-end (#6631). printer.fern's own round-trip
	// assertions cover parse → print → parse; what they cannot show is that
	// the annotation survives the REAL driver, whose module pipeline runs the
	// AST-rewrite passes (label resolution, range desugar, defer lowering)
	// that each rebuild a FuncDecl field by field. A copy site hardcoding
	// `async: false` drops the bit there and nowhere else.
	t.Run("async-function-through-the-driver", func(t *testing.T) {
		src := "async function work(): i32 { return 7; }\nfunction main(): i32 { return work(); }\n"
		path := filepath.Join(dir, "async_prog.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		out, code := runDriver(t, "-fmt", path)
		if code != 0 {
			t.Fatalf("-fmt exit = %d, want 0 (out: %s)", code, out)
		}
		if !strings.HasPrefix(string(out), "async function work") {
			t.Errorf("-fmt dropped the async modifier; got:\n%s", out)
		}
		// And the annotated function still runs — `async` is a marker, so the
		// body must behave exactly as an unannotated one.
		if _, code := runDriver(t, "-interp", path); code != 7 {
			t.Errorf("-interp exit = %d, want 7", code)
		}
		// `-check` has to be clean on BOTH channels. A modifier that is not
		// recognised at the decl dispatch does not fail the parse: it falls
		// through to `parse_stmt` as a bare identifier expression and the
		// declaration behind it parses normally, so the run still exits 0
		// while the checker reports `undefined name "async"` on stderr.
		// Exit status alone therefore proves nothing here (#6659).
		checkCmd := exec.Command(fernBin, "-check", path)
		var checkErr bytes.Buffer
		checkCmd.Stderr = &checkErr
		if err := checkCmd.Run(); err != nil {
			t.Errorf("-check failed: %v (stderr: %s)", err, checkErr.String())
		}
		if checkErr.Len() != 0 {
			t.Errorf("-check exited 0 but wrote diagnostics:\n%s", checkErr.String())
		}
		// Contextual: `async` in any other position stays an identifier.
		identPath := filepath.Join(dir, "async_ident.fern")
		identSrc := "function main(): i32 { var async: i32 = 3; return async + 4; }\n"
		if err := os.WriteFile(identPath, []byte(identSrc), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, code := runDriver(t, "-interp", identPath); code != 7 {
			t.Errorf("`async` as an identifier: exit = %d, want 7", code)
		}
	})

	// The u32 decimal formatter is reachable ONLY through this driver. It needs
	// `import "std/u32"` (or std/string, which uses it), and every other
	// self-host wasm driver — wasm_run, wasm_ir_run, wasm_modload_run — parses
	// raw source with no module loader, so the import never resolves there and
	// the call is never emitted. That is exactly why #5992 recorded this as
	// "to_lower is not IR-eligible on wasm": a no-modload driver reported `ast`
	// for a program it could not resolve, and the real failure was one layer
	// further on.
	//
	// The real failure: the register backends compile __fern_u32_to_string from
	// asmcore.rt_src_u32_to_string, wasm had no body and no name mapping for it,
	// so the emitted module CALLED a function it never DEFINED. That is an
	// instantiation error, not a lowering bail — the module compiles clean and
	// `-decide` says "ir" — so only running it catches the regression.
	t.Run("wasm-u32-to-string-is-defined", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping u32-to-string wasm check")
		}
		stdlibRoot, err := filepath.Abs("../../internal/stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		src := `import "std/u32";

function main(): i32 {
    var u: u32 = 3000000000 as u32;
    var s: string = u.to_string();
    if (s == "3000000000") { return 42; }
    return 7;
}
`
		srcPath := filepath.Join(dir, "u32_to_string.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath, stdlibRoot)
		if code != 0 {
			t.Fatalf("-target wasm emit exited %d, want 0", code)
		}
		if !bytes.Contains(wat, []byte("$__fern_u32_to_str")) {
			t.Error("emitted WAT never defines/calls $__fern_u32_to_str")
		}
		if bytes.Contains(wat, []byte("call $__fern_u32_to_string")) {
			t.Error("emitted WAT calls the unmapped $__fern_u32_to_string — that name has no body (#5992)")
		}
		watPath := filepath.Join(dir, "u32_to_string.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.CombinedOutput()
		// 3000000000 has bit 31 set: a signed formatter prints it negative, so
		// the string compare fails and this returns 7 rather than 42.
		if got := cmd.ProcessState.ExitCode(); got != 42 {
			t.Errorf("u32.to_string() on wasm exited %d, want 42\n%s", got, out)
		}
	})

	// Two traits may each provide a method of the same name for one type
	// (#6931). The self-host emits a method as the flat `<Type>.<name>`
	// symbol, so without disambiguation both providers land on `$P.scale` —
	// a WAT text module with two `(func $P.scale …)` is rejected outright by
	// wasmtime ("duplicate func identifier"), and the register backends
	// silently keep whichever they saw first. parser.claim_method_name
	// interposes the second provider's trait; these assert the emitted symbol
	// namespace is injective and that the surviving definition is the FIRST
	// provider's, which is what every call site resolves to.
	t.Run("two-traits-one-method-emits-one-symbol", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping duplicate-method-symbol check")
		}
		stdlibRoot, err := filepath.Abs("../../internal/stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		src := `import "std/i32";

struct P { x: i32 }

trait A { function scale(self: Self): i32; }
trait B { function scale(self: Self): i32; }

impl A for P { function scale(self: Self): i32 { return self.x; } }
impl B for P { function scale(self: Self): i32 { return 100; } }

function twice[T: A](v: T): i32 { return v.scale() + v.scale(); }

function main(): i32 {
    var v: P = P { x: 5 };
    return twice(v);
}
`
		srcPath := filepath.Join(dir, "two_traits_one_method.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath, stdlibRoot)
		if code != 0 {
			t.Fatalf("-target wasm emit exited %d, want 0", code)
		}
		if got := bytes.Count(wat, []byte("(func $P.scale ")); got != 1 {
			t.Errorf("emitted WAT defines $P.scale %d times, want exactly 1", got)
		}
		if !bytes.Contains(wat, []byte("(func $P.B.scale ")) {
			t.Error("B's provider was not interposed with its trait; expected a $P.B.scale definition")
		}
		watPath := filepath.Join(dir, "two_traits_one_method.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.CombinedOutput()
		// A's `scale` is `self.x`, so 5 + 5. Native resolves the call through
		// the `T: A` bound to the same answer.
		if got := cmd.ProcessState.ExitCode(); got != 10 {
			t.Errorf("twice(P{x:5}) on wasm exited %d, want 10\n%s", got, out)
		}
	})

	// The cross-module shape of the same collision: std/num's `impl Add for
	// i32` and a user trait's `add` for i32 both want the symbol `i32.add`.
	// mangle_module deliberately leaves method NAMES alone, so only
	// flatten.disambiguate_methods separates them — the first module to claim
	// the key keeps it, and the entry (bundled last) is interposed with its
	// namespace.
	t.Run("cross-module-method-collision-emits-one-symbol", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping cross-module collision check")
		}
		stdlibRoot, err := filepath.Abs("../../internal/stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		src := `import "std/num";

trait MyAdd { function add(self: Self, o: Self): Self; }
impl MyAdd for i32 { function add(self: Self, o: Self): Self { return self + o + 1; } }

function sum[T: num.Add](a: T, b: T): T { return a.add(b); }

function main(): i32 { return sum(2, 3); }
`
		srcPath := filepath.Join(dir, "cross_module_add.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath, stdlibRoot)
		if code != 0 {
			t.Fatalf("-target wasm emit exited %d, want 0", code)
		}
		if got := bytes.Count(wat, []byte("(func $i32.add ")); got != 1 {
			t.Errorf("emitted WAT defines $i32.add %d times, want exactly 1", got)
		}
		if !bytes.Contains(wat, []byte("(func $i32.__entry.add ")) {
			t.Error("the entry's colliding provider was not interposed with its namespace")
		}
		watPath := filepath.Join(dir, "cross_module_add.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.CombinedOutput()
		// std/num is bundled before the entry, so its `Add` claims `i32.add`
		// and `sum`'s `a.add(b)` reaches it: 2 + 3.
		if got := cmd.ProcessState.ExitCode(); got != 5 {
			t.Errorf("sum(2, 3) on wasm exited %d, want 5\n%s", got, out)
		}
	})

	// `dyn B` over a type whose `m` comes from BOTH A and B. The arm chain is
	// keyed on the method name, so once B's provider is emitted as `S.B.m` a
	// search for a bare `m` finds only A's — the trait set travels on
	// op_dyn_dispatch so the arm resolves to the provider the dyn names. Both
	// directions are asserted: a fix that merely flips which provider wins
	// fails the `dyn A` case.
	t.Run("dyn-picks-the-named-trait-provider", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping dyn provider check")
		}
		const decls = `trait A { function m(self: Self): i32; }
trait B { function m(self: Self): i32; }
struct S { v: i32 }
impl A for S { function m(self: Self): i32 { return self.v; } }
impl B for S { function m(self: Self): i32 { return 7; } }
`
		for _, tc := range []struct {
			name string
			dyn  string
			want int
		}{
			// B's provider is the interposed one; A's keeps the bare name.
			{"dyn-b", "dyn B", 7},
			{"dyn-a", "dyn A", 3},
		} {
			t.Run(tc.name, func(t *testing.T) {
				src := decls + "function main(): i32 {\n    var d: " + tc.dyn + " = S { v: 3 };\n    return d.m();\n}\n"
				srcPath := filepath.Join(dir, "dyn_"+tc.name+".fern")
				if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath)
				if code != 0 {
					t.Fatalf("-target wasm emit exited %d, want 0", code)
				}
				watPath := filepath.Join(dir, "dyn_"+tc.name+".wat")
				if err := os.WriteFile(watPath, wat, 0o644); err != nil {
					t.Fatalf("write wat: %v", err)
				}
				cmd := exec.Command("wasmtime", "run", watPath)
				out, _ := cmd.CombinedOutput()
				if got := cmd.ProcessState.ExitCode(); got != tc.want {
					t.Errorf("`%s` dispatch exited %d, want %d\n%s", tc.dyn, got, tc.want, out)
				}
			})
		}
		// A dyn set whose traits declare DIFFERENT method names must keep
		// resolving through the bare name — nothing was interposed there.
		t.Run("multi-trait-distinct-names", func(t *testing.T) {
			src := `trait A { function m(self: Self): i32; }
trait B { function n(self: Self): i32; }
struct S { v: i32 }
impl A for S { function m(self: Self): i32 { return self.v; } }
impl B for S { function n(self: Self): i32 { return 7; } }
function main(): i32 {
    var d: dyn A + B = S { v: 3 };
    return d.m() + d.n();
}
`
			srcPath := filepath.Join(dir, "dyn_multi.fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath)
			if code != 0 {
				t.Fatalf("-target wasm emit exited %d, want 0", code)
			}
			watPath := filepath.Join(dir, "dyn_multi.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			out, _ := cmd.CombinedOutput()
			if got := cmd.ProcessState.ExitCode(); got != 10 {
				t.Errorf("`dyn A + B` dispatch exited %d, want 10\n%s", got, out)
			}
		})
	})

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
		// An array program is in the SSA subset on x86-64: the opt-in -ssa
		// path compiles it through the heap-aware backend. The IR baseline
		// is the default (== -no-ssa) now that the IR path is production and
		// -ssa is opt-in (issue #4391). The -ssa output must differ from it.
		srcPath := filepath.Join(dir, "ssa_arr.fern")
		src := "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		ssaAsm, code := runDriver(t, "-ssa", srcPath)
		if code != 0 {
			t.Fatalf("emit exited %d, want 0", code)
		}
		baseAsm, _ := runDriver(t, "-no-ssa", srcPath)
		if string(ssaAsm) == string(baseAsm) {
			t.Error("-ssa fell back to the IR path for an array program (expected the SSA heap backend)")
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
		// rejects the call and the whole program falls back to the IR path.
		// Asserting the opt-in -ssa output differs from -no-ssa proves the SSA
		// path (with injected helpers) was taken.
		srcPath := filepath.Join(dir, "ssa_helpers.fern")
		src := "function main(): i32 { var a = [1, 2]; a = a.append(3); var b = a[1:3]; return b[0] + b[1] + b.len() + a.len(); }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		ssaAsm, code := runDriver(t, "-ssa", srcPath)
		if code != 0 {
			t.Fatalf("emit exited %d, want 0", code)
		}
		baseAsm, _ := runDriver(t, "-no-ssa", srcPath)
		if string(ssaAsm) == string(baseAsm) {
			t.Error("-ssa fell back to the IR path for a push/slice program (runtime helpers not injected)")
		}
		progBin := buildBin(t, gcc, dir, "ssa_helpers", string(ssaAsm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 10 {
			t.Errorf("-ssa push/slice program exited %d, want 10", c)
		}
	})

	t.Run("ssa-opt-in", func(t *testing.T) {
		// The IR path is the DEFAULT now; SSA is opt-in via -ssa (issue #4391).
		// For an in-subset program the default output must equal the -no-ssa
		// output and differ from the explicit -ssa output — proving the
		// default routes through the IR path and -ssa opts into the SSA backend.
		srcPath := filepath.Join(dir, "default_ir.fern")
		src := "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		def, code := runDriver(t, srcPath)
		if code != 0 {
			t.Fatalf("default emit exited %d, want 0", code)
		}
		baseline, _ := runDriver(t, "-no-ssa", srcPath)
		if string(def) != string(baseline) {
			t.Error("default codegen differs from -no-ssa (IR path is not the default)")
		}
		explicit, _ := runDriver(t, "-ssa", srcPath)
		if string(def) == string(explicit) {
			t.Error("default codegen equals -ssa for an in-subset program (default incorrectly used SSA)")
		}
	})

	t.Run("ssa-fallback", func(t *testing.T) {
		// A program outside the SSA subset falls back from -ssa to the IR path
		// transparently: the -ssa output is byte-identical to the -no-ssa
		// output, so opting into SSA never emits wrong code for programs it
		// can't yet lower. An `enum` match is one such construct build_func
		// still declines (floats, struct spread, and int-literal match — the
		// latter desugared to if/else by the parser — now lower through SSA).
		srcPath := filepath.Join(dir, "fallback.fern")
		if err := os.WriteFile(srcPath, []byte("enum Color { Red, Green } function main(): i32 { var c: Color = Green; match (c) { Red => { return 1; }, Green => { return 2; }, _ => { return 0; } } }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		ssaOut, code1 := runDriver(t, "-ssa", srcPath)
		baseline, code2 := runDriver(t, "-no-ssa", srcPath)
		if code1 != 0 || code2 != 0 {
			t.Fatalf("emit exited %d / %d, want 0", code1, code2)
		}
		if string(ssaOut) != string(baseline) {
			t.Errorf("-ssa did not fall back cleanly for an out-of-subset program (output differs from -no-ssa)")
		}
	})

	t.Run("emit-ssa-wasm", func(t *testing.T) {
		// The wasm target is the third SSA consumer (ssa_wasm.fern), now
		// covering the whole SSA subset: integer, heap (array), string-concat,
		// print, and closures all compile through the SSA backend when opted in
		// via -ssa — each program's WAT differs from the -no-ssa (IR emitter)
		// output and runs to its value / output. A program outside the subset (a
		// float local) still falls back via the supported() gate (identical WAT).
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping -target wasm SSA path")
		}
		runWat := func(t *testing.T, name string, wat []byte) int {
			t.Helper()
			watPath := filepath.Join(dir, name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			_ = cmd.Run()
			return cmd.ProcessState.ExitCode()
		}

		// Core program: -ssa WAT differs from the -no-ssa (IR) WAT.
		coreSrc := "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { var s = 0; var i = 0; while (i < 10) { s = s + fib(i); i = i + 1; } return s; }\n"
		corePath := filepath.Join(dir, "wasm_core.fern")
		if err := os.WriteFile(corePath, []byte(coreSrc), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		ssaWat, code := runDriver(t, "-ssa", "-target", "wasm32-wasi", "-emit", "asm", corePath)
		if code != 0 {
			t.Fatalf("-target wasm emit exited %d, want 0", code)
		}
		baseWat, _ := runDriver(t, "-no-ssa", "-target", "wasm32-wasi", "-emit", "asm", corePath)
		if string(ssaWat) == string(baseWat) {
			t.Error("-ssa -target wasm fell back to the IR path for a core program (expected the SSA wasm backend)")
		}
		if got := runWat(t, "wasm_core", ssaWat); got != 88 {
			t.Errorf("SSA wasm core program exited %d, want 88", got)
		}

		// Heap program (array): the SSA wasm backend covers alloc / load_elem
		// / store_elem now, so this also compiles through SSA — WAT differs
		// from the -no-ssa output and runs to its value.
		arrSrc := "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }\n"
		arrPath := filepath.Join(dir, "wasm_arr.fern")
		if err := os.WriteFile(arrPath, []byte(arrSrc), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		defArr, _ := runDriver(t, "-ssa", "-target", "wasm32-wasi", "-emit", "asm", arrPath)
		baseArr, _ := runDriver(t, "-no-ssa", "-target", "wasm32-wasi", "-emit", "asm", arrPath)
		if string(defArr) == string(baseArr) {
			t.Error("-ssa -target wasm fell back to the IR path for an array program (expected the SSA heap backend)")
		}
		if got := runWat(t, "wasm_arr", defArr); got != 75 {
			t.Errorf("SSA wasm array program exited %d, want 75", got)
		}

		// String build (concat) compiles through SSA now: WAT differs from the
		// -no-ssa output and runs to its value.
		catSrc := "function main(): i32 { var a = \"foo\"; var b = \"bar\"; var c = a + b; return c.len(); }\n"
		catPath := filepath.Join(dir, "wasm_cat.fern")
		if err := os.WriteFile(catPath, []byte(catSrc), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		defCat, _ := runDriver(t, "-ssa", "-target", "wasm32-wasi", "-emit", "asm", catPath)
		baseCat, _ := runDriver(t, "-no-ssa", "-target", "wasm32-wasi", "-emit", "asm", catPath)
		if string(defCat) == string(baseCat) {
			t.Error("-ssa -target wasm fell back to the IR path for a string-concat program (expected the SSA concat helper)")
		}
		if got := runWat(t, "wasm_cat", defCat); got != 6 {
			t.Errorf("SSA wasm string-concat program exited %d, want 6", got)
		}

		// print compiles through SSA now and appends a trailing newline (Fern's
		// print semantics), matching the IR path. WAT differs from -no-ssa,
		// exits 42, and writes "hi from wasm\n" to stdout.
		prSrc := "function main(): i32 { print(\"hi from wasm\"); return 6 * 7; }\n"
		prPath := filepath.Join(dir, "wasm_print.fern")
		if err := os.WriteFile(prPath, []byte(prSrc), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		defPr, _ := runDriver(t, "-ssa", "-target", "wasm32-wasi", "-emit", "asm", prPath)
		basePr, _ := runDriver(t, "-no-ssa", "-target", "wasm32-wasi", "-emit", "asm", prPath)
		if string(defPr) == string(basePr) {
			t.Error("-ssa -target wasm fell back to the IR path for a print program (expected the SSA print helper)")
		}
		prWatPath := filepath.Join(dir, "wasm_print.wat")
		if err := os.WriteFile(prWatPath, defPr, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		prCmd := exec.Command("wasmtime", "run", prWatPath)
		prOut, _ := prCmd.Output()
		if c := prCmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("SSA wasm print program exited %d, want 42", c)
		}
		if string(prOut) != "hi from wasm\n" {
			t.Errorf("SSA wasm print stdout = %q, want %q", string(prOut), "hi from wasm\n")
		}

		// Closures compile through SSA now (function table + call_indirect):
		// a capturing lambda's WAT differs from -no-ssa and runs correctly.
		capSrc := "function main(): i32 { var n = 10; var f = function (x: i32): i32 { return x + n; }; return f(5); }\n"
		capPath := filepath.Join(dir, "wasm_cap.fern")
		if err := os.WriteFile(capPath, []byte(capSrc), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		defCap, _ := runDriver(t, "-ssa", "-target", "wasm32-wasi", "-emit", "asm", capPath)
		baseCap, _ := runDriver(t, "-no-ssa", "-target", "wasm32-wasi", "-emit", "asm", capPath)
		if string(defCap) == string(baseCap) {
			t.Error("-ssa -target wasm fell back to the IR path for a capturing-lambda program (expected the SSA closure path)")
		}
		if got := runWat(t, "wasm_cap", defCap); got != 15 {
			t.Errorf("SSA wasm capturing-lambda program exited %d, want 15", got)
		}

		// Regression: try_ssa must not mutate the shared AST. A `while (true)`
		// loop with all returns inside it forces the fallback (build_func bails
		// on the missing trailing return), but collect_lambdas still runs in
		// try_ssa first; it used to append `__env` to the lambda's params in
		// place, corrupting the AST the fallback emitter then reused (duplicate
		// $__env → invalid WAT). Guard it: a while-true + capturing-lambda
		// program falls back to the IR path (-ssa WAT == -no-ssa WAT) and
		// runs correctly.
		fbSrc := "function main(): i32 { var n = 5; var f = function (z: i32): i32 { return z + n; }; var r = f(2); var i = 0; while (true) { i = i + 1; if (i >= r) { return r; } } }\n"
		fbPath := filepath.Join(dir, "wasm_fb.fern")
		if err := os.WriteFile(fbPath, []byte(fbSrc), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		defFb, _ := runDriver(t, "-ssa", "-target", "wasm32-wasi", "-emit", "asm", fbPath)
		baseFb, _ := runDriver(t, "-no-ssa", "-target", "wasm32-wasi", "-emit", "asm", fbPath)
		if string(defFb) != string(baseFb) {
			t.Error("-ssa -target wasm corrupted the AST for a fallback program (try_ssa has a side effect)")
		}
		if got := runWat(t, "wasm_fb", defFb); got != 7 {
			t.Errorf("while-true+lambda fallback program exited %d, want 7", got)
		}
	})

	// Constant folding is NOT what `-O` selects, on either compiler. Native
	// calls constfold.Fold unconditionally on all four of its compile paths and
	// spends `-O` on eliding asserts; the self-host now matches. So every case
	// below asserts the DEFAULT build folds, and that `-O` changes nothing —
	// which is what fails if the pass is ever put back behind a flag.
	foldCase := func(t *testing.T, name, src string, wantExit int, unwanted ...string) {
		t.Helper()
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		defAsm, code := runDriver(t, "-no-ssa", srcPath)
		if code != 0 {
			t.Fatalf("default emit exited %d, want 0", code)
		}
		for _, u := range unwanted {
			if n := strings.Count(string(defAsm), u); n != 0 {
				t.Errorf("%s: the DEFAULT build left %d %q; constant folding is gated again:\n%s", name, n, u, defAsm)
			}
		}
		optAsm, code := runDriver(t, "-no-ssa", "-O", srcPath)
		if code != 0 {
			t.Fatalf("-O emit exited %d, want 0", code)
		}
		if string(optAsm) != string(defAsm) {
			t.Errorf("%s: -O changed the emitted code; folding is meant to run on every build\n"+
				"--- -O ---\n%s\n--- default ---\n%s", name, optAsm, defAsm)
		}
		progBin := buildBin(t, gcc, dir, name, string(defAsm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != wantExit {
			t.Errorf("%s exited %d, want %d", name, c, wantExit)
		}
	}

	// Arithmetic. The op-list fold in ir.fern reaches this one downstream too,
	// so the `unwanted` mnemonics are what distinguishes "folded" from "both
	// paths quietly failed to fold".
	t.Run("fold-constants", func(t *testing.T) {
		foldCase(t, "fold", "function main(): i32 { return 2 * 3 + 1; }\n", 7, "imulq", "addq %rcx, %rax")
	})

	// A literal concat is the shape the op-list fold CANNOT reach — it has no
	// string vocabulary — so this is the case that actually shows the AST fold
	// running. It was the open half of #7111: before ungating, a default build
	// emitted a runtime __fern_str_concat call where native emitted one literal.
	t.Run("fold-literal-concat", func(t *testing.T) {
		foldCase(t, "foldconcat",
			"function main(): i32 { var s: string = \"ab\" + \"cd\"; return s.len(); }\n",
			4, "call __fn___fern_str_concat")
	})

	// A match arm's GUARD rides the ARM, not the statement's own expressions,
	// so a rewrite that rebuilt arms field by field had to remember it —
	// constfold's did not, and copied every guard through unfolded until it was
	// routed through astwalk's map fold.
	t.Run("fold-guard-concat", func(t *testing.T) {
		foldCase(t, "foldguard", "enum E { A(string) }\n"+
			"function main(): i32 {\n"+
			"    var e: E = E.A(\"abcd\");\n"+
			"    match (e) {\n"+
			"        E.A(s) when s == (\"ab\" + \"cd\") => { return 7; },\n"+
			"        _ => { return 9; },\n"+
			"    }\n"+
			"    return 0;\n"+
			"}\n", 7, "call __fn___fern_str_concat")
	})

	// i32::MIN. `0 - 2147483647 - 1` is how std/i32 spells it, because it is the
	// only way to spell it — and folding it produced an ExprNumber whose TEXT
	// carried the sign. irlower.literal_is_i64 classifies a literal by text
	// LENGTH, so the 11-character "-2147483648" read as an i64 and the i32
	// return path declined it: the whole module fell off the IR path. lit_i32
	// now emits unary minus over the magnitude, the shape the parser produces.
	//
	// This failed under `-O` long before folding was ungated; nothing ran `-O`
	// over a program reaching std/i32, so it sat unfound.
	t.Run("fold-i32-min", func(t *testing.T) {
		foldCase(t, "foldmin", "function f(): i32 { return 0 - 2147483647 - 1; }\n"+
			"function main(): i32 { if (f() < 0 - 2147483647) { return 11; } return 1; }\n", 11)
	})

	// A hex operand in a folded binop must carry its value (#4341): as_int_lit
	// used the decimal-only digits_to_i32, which stops at the `x` and read every
	// `0x` literal as 0, folding `0x10 + 1` to 1 — a silent wrong answer rather
	// than a missed optimisation.
	t.Run("fold-hex-constants", func(t *testing.T) {
		foldCase(t, "foldhex", "function main(): i32 { return 0x10 + 1; }\n", 17)
	})

	// `-O` is the release build: it elides every assert() check and nothing
	// else, which is the one thing native spends the flag on
	// (constfold.ElideAsserts). assertCase pins both directions — the default
	// build still checks, `-O` does not — because an elision that fired on
	// every build would be a silent loss of every assert in the language.
	assertCase := func(t *testing.T, name, src string, wantDefault, wantOpt int) {
		t.Helper()
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		for _, leg := range []struct {
			label    string
			args     []string
			wantExit int
			wantMsg  int
		}{
			{"default", []string{"-no-ssa"}, wantDefault, 1},
			{"-O", []string{"-no-ssa", "-O"}, wantOpt, 0},
		} {
			asm, code := runDriver(t, append(append([]string{}, leg.args...), srcPath)...)
			if code != 0 {
				t.Fatalf("%s: %s emit exited %d, want 0", name, leg.label, code)
			}
			// The desugar's message is the only trace an assert leaves in the
			// emitted code, so its absence is what "elided" means.
			if n := strings.Count(string(asm), "assertion failed"); (n > 0) != (leg.wantMsg > 0) {
				t.Errorf("%s: %s build has %d \"assertion failed\" string(s), want %s:\n%s",
					name, leg.label, n, map[bool]string{true: "at least one", false: "none"}[leg.wantMsg > 0], asm)
			}
			progBin := buildBin(t, gcc, dir, name+leg.label, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if c := cmd.ProcessState.ExitCode(); c != leg.wantExit {
				t.Errorf("%s: %s build exited %d, want %d", name, leg.label, c, leg.wantExit)
			}
		}
	}

	// A failing assert: exit 1 on a default build, gone under `-O` so the
	// function reaches its return. Native answers 1 / 5 on the same program.
	t.Run("opt-elides-assert", func(t *testing.T) {
		assertCase(t, "elide", "function main(): i32 {\n"+
			"    var n: i32 = 3;\n"+
			"    assert(n > 100, \"boom\");\n"+
			"    return 5;\n"+
			"}\n", 1, 5)
	})

	// The shapes a top-level statement filter misses. Each nests an assert one
	// level deeper than the last; `-O` has to reach all five, which is what
	// routing the pass through astwalk's splicing fold buys — a lambda body
	// and a match arm are not in the enclosing statement list at all.
	t.Run("opt-elides-nested-asserts", func(t *testing.T) {
		assertCase(t, "elidenest", "enum E { A(i32) }\n"+
			"function run(f: () => i32): i32 { return f(); }\n"+
			"function main(): i32 {\n"+
			"    var n: i32 = 3;\n"+
			"    if (n > 0) { assert(n > 100, \"nested-if\"); }\n"+
			"    var g: i32 = 0;\n"+
			"    while (g < 1) { assert(n > 100, \"while\"); g = g + 1; }\n"+
			"    var r: i32 = run(function(): i32 { assert(n > 100, \"lambda\"); return 1; });\n"+
			"    var e: E = E.A(1);\n"+
			"    match (e) {\n"+
			"        E.A(v) => { assert(v > 100, \"match-arm\"); },\n"+
			"        _ => { return 90; }\n"+
			"    }\n"+
			"    var r2: i32 = run(function(): i32 {\n"+
			"        return run(function(): i32 { assert(n > 100, \"nested-lambda\"); return 2; });\n"+
			"    });\n"+
			"    return r + r2 + 2;\n"+
			"}\n", 1, 5)
	})

	// Through MONOMORPHISATION. The marker rides StmtIf.origin, and the passes
	// between the parser and the elision rebuild statements field by field —
	// a copy site that omits `origin` drops it silently and only for cloned
	// generics, which is the same shape as the async-function case above.
	// Both instantiations must lose their assert.
	t.Run("opt-elides-assert-in-generic", func(t *testing.T) {
		assertCase(t, "elidegen", "function check_it[T](v: T, n: i32): i32 {\n"+
			"    assert(n > 100, \"generic\");\n"+
			"    return 1;\n"+
			"}\n"+
			"function main(): i32 {\n"+
			"    return check_it(7, 3) + check_it(\"s\", 3) + 3;\n"+
			"}\n", 1, 5)
	})

	// Ordering: the elision runs AFTER the gates, so a release build rejects
	// the same programs a default build does rather than deleting the offending
	// code unexamined. `-O` must narrow the emitted program, never widen the
	// set that compiles.
	//
	// The target-capability gate (E066) is the shape this can be pinned with.
	// The checker's own diagnostics cannot: every code an ill-typed assert
	// raises — E001, E004, E009, E043 — is on is_partial_checker_gap_code's
	// measured list, so none of them gates a build here yet. See #7170.
	t.Run("opt-elides-after-the-gates", func(t *testing.T) {
		srcPath := filepath.Join(dir, "capassert.fern")
		src := "function main(): i32 {\n    assert(proc_fork() >= 0);\n    return 0;\n}\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		for _, leg := range [][]string{
			{"-target", "wasm32-wasi", "-emit", "asm", srcPath},
			{"-target", "wasm32-wasi", "-emit", "asm", "-O", srcPath},
		} {
			out, code := runDriver(t, leg...)
			if code == 0 {
				t.Errorf("%v: a capability-violating assert compiled; the elision is running before the gates:\n%s", leg, out)
			}
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

	t.Run("payloadless-variant-as-payload-binder", func(t *testing.T) {
		// #2698: a bare payload-LESS variant name in a payload slot parses as
		// a BINDER, so `Wrap(Er2)` matched every payload and the arm returned
		// its answer for every value. Native gained the rejection first; the
		// self-host did not, which turned a shared bug into a divergence —
		// native refusing the program while the self-host compiled it and
		// answered 9 where 1 is correct. Both must now refuse it, with the
		// same code, and must still accept the two spellings that are fine:
		// an ordinary binder, and the empty-paren form that tests the tag.
		decls := "enum Res2 { Ok2(i32), Er2 }\nenum Nest3 { Wrap(Res2), Bare }\n"
		bad := decls + "function g(w: Nest3): i32 { match (w) { Wrap(Er2) => { return 9; }, _ => { return 1; } } }\nfunction main(): i32 { return g(Nest3.Wrap(Res2.Ok2(5))); }\n"
		badPath := filepath.Join(dir, "plbinder_bad.fern")
		if err := os.WriteFile(badPath, []byte(bad), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", badPath); code != 1 {
			t.Errorf("self-host -check on a payload-less-variant binder exited %d, want 1", code)
		}
		combined, _ := exec.Command(fernBin, "-check", badPath).CombinedOutput()
		if !strings.Contains(string(combined), "error[E015]") {
			t.Errorf("self-host -check diagnostics = %q, want error[E015]", combined)
		}
		if !strings.Contains(string(combined), "Er2()") {
			t.Errorf("diagnostic must name the spelling that works: %q", combined)
		}

		// An ordinary binder, and a binder whose name belongs to a DIFFERENT
		// enum, are both untouched — the rule is scoped to the enum the slot
		// actually holds, as native's is.
		ok := decls + "enum Other { Zed, Yy(i32) }\nfunction g(w: Nest3): i32 { match (w) { Wrap(x) => { return 1; }, Bare => { return 2; } } }\nfunction h(w: Nest3): i32 { match (w) { Wrap(Zed) => { return 1; }, Bare => { return 2; } } }\nfunction main(): i32 { return g(Nest3.Bare) + h(Nest3.Bare); }\n"
		okPath := filepath.Join(dir, "plbinder_ok.fern")
		if err := os.WriteFile(okPath, []byte(ok), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		okOut, _ := exec.Command(fernBin, "-check", okPath).CombinedOutput()
		if strings.Contains(string(okOut), "error[E015]") {
			t.Errorf("rule fired on a legitimate binder: %q", okOut)
		}
	})

	t.Run("float-literal-out-of-range", func(t *testing.T) {
		// #6842: a float literal too large for f64 is rejected by the front
		// end, as native's parser does, instead of compiling to +Inf. All
		// three channels have to agree, because they gate independently:
		// `-check` (the checker's P002), COMPILE (the pre-codegen build gate,
		// which used to filter the diagnostic set down to the immutability
		// rules and let this through on every target), and `-interp`.
		srcPath := filepath.Join(dir, "float_oor.fern")
		src := "function main(): i32 { var x: f64 = 1e309; if (x > 1.0e308) { return 7; } return 0; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		for _, mode := range [][]string{{"-check"}, {"-interp"}, {"-target", "x86-64-linux", "-emit", "asm"}} {
			cmd := exec.Command(fernBin, append(append([]string{}, mode...), srcPath)...)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code == 0 {
				t.Errorf("%v: exited 0, want a rejection (out: %s)", mode, out)
			}
			// The interp reports the parser-side rejection as a bottom value,
			// not a coded diagnostic; the two gating modes name P002.
			if mode[0] != "-interp" && !strings.Contains(string(out), "error[P002]") {
				t.Errorf("%v: diagnostics = %q, want error[P002]", mode, out)
			}
		}
		// `-fmt` is the mode that must NOT reject it: a reformat has to return
		// the text it was handed, so the verbatim parse keeps the literal
		// (printer.fern pins the round-trip directly).
		fmtOut, code := runDriver(t, "-fmt", srcPath)
		if code != 0 {
			t.Errorf("-fmt exited %d, want 0 (out: %s)", code, fmtOut)
		}
		if !strings.Contains(string(fmtOut), "1e309") {
			t.Errorf("-fmt lost the literal; got:\n%s", fmtOut)
		}
		// The accepted side of the same boundary: underflow is not a range
		// error on either engine, so this compiles and runs.
		okPath := filepath.Join(dir, "float_underflow.fern")
		okSrc := "function main(): i32 { var x: f64 = 1e-400; if (x == 0.0) { return 7; } return 0; }\n"
		if err := os.WriteFile(okPath, []byte(okSrc), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", okPath); code != 0 {
			t.Errorf("-check on an underflowing literal exited %d, want 0", code)
		}
		asm, code := runDriver(t, okPath)
		if code != 0 {
			t.Fatalf("emit on an underflowing literal exited %d, want 0", code)
		}
		progBin := buildBin(t, gcc, dir, "float_underflow", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 7 {
			t.Errorf("underflowing-literal program exited %d, want 7", c)
		}
	})

	t.Run("check-enum-value-ok", func(t *testing.T) {
		// #4346 piece 2 (enum-value slice): a bare no-payload enum variant used
		// as a value (`var c: Color = Red;`) types to its enum's union, so this
		// native-valid program now passes self-host `-check` (exit 0). Before
		// the slice `Red` typed to unknown, failed `type_assignable` against the
		// declared `Color`, and `-check` exited 1 (a silent over-reject). Covers
		// both the assignment and return positions.
		srcPath := filepath.Join(dir, "enum_value.fern")
		src := "enum Color { Red, Green }\n" +
			"function pick(): Color { return Green; }\n" +
			"function main(): i32 { var c: Color = Red; var d: Color = pick(); return 0; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on an enum-value program exited %d, want 0", code)
		}
	})

	t.Run("check-option-result-value-ok", func(t *testing.T) {
		// #4346 piece 2 (Option/Result slice): a builtin `Option[i32]` /
		// `Result[i32, i32]` annotation resolves to a name-only union, and the
		// constructors `Some(x)` / `None` / `Ok(x)` / `Err(e)` type to that
		// union — so this native-valid program passes self-host `-check`
		// (exit 0). Before the slice each collapsed to unknown and `-check`
		// exited 1 (a silent over-reject).
		srcPath := filepath.Join(dir, "option_result_value.fern")
		src := "function main(): i32 {\n" +
			"    var a: Option[i32] = Some(3);\n" +
			"    var b: Option[i32] = None;\n" +
			"    var c: Result[i32, i32] = Ok(3);\n" +
			"    var d: Result[i32, i32] = Err(9);\n" +
			"    return 0;\n" +
			"}\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on an Option/Result-value program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-body-ok", func(t *testing.T) {
		// #4346 piece 2 (generic-body slice): a generic function's body operates
		// on opaque-typed values (`v: T` erases to unknown), which is expected,
		// not an error — native accepts it. check_func_body no longer marks a
		// generic body ill-typed on a TypeUnknown statement, so this native-valid
		// declared-generic program passes self-host `-check` (exit 0) instead of
		// the prior silent over-reject exit 1 — matching the issue's
		// "declared-but-unused generic" example. (CALLING a generic and using its
		// result, `return ident(3)`, still yields unknown at a non-generic call
		// site: generic-call return-type inference is a separate future slice.)
		srcPath := filepath.Join(dir, "generic_body.fern")
		if err := os.WriteFile(srcPath, []byte("function ident[T](v: T): T { return v; }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a declared-generic program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-call-ok", func(t *testing.T) {
		// #4346 piece 2 (generic-call slice): CALLING a generic function and
		// using its result now type-checks — the return type is inferred from
		// the argument whose parameter shares the return's type-parameter name
		// (`ident(3)` → i32, so `return ident(3)` matches main's i32 return).
		// Before the slice the call typed to unknown and a non-generic caller
		// over-rejected (exit 1).
		srcPath := filepath.Join(dir, "generic_call.fern")
		if err := os.WriteFile(srcPath, []byte("function ident[T](v: T): T { return v; }\nfunction main(): i32 { return ident(3); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a generic-call program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-struct-ok", func(t *testing.T) {
		// #4346 piece 2 (generic-struct slice): a user generic-struct
		// annotation `Box[i32]` resolves to the name-only struct `Box`, and its
		// literal `Box { v: 3 }` type-checks (the opaque generic field `v: T`
		// accepts any value), so constructing and binding one passes self-host
		// `-check` (exit 0) instead of the prior silent over-reject exit 1.
		// (Field access `b.v` still yields unknown — the field's type parameter
		// isn't substituted yet — so it's deliberately not exercised here.)
		srcPath := filepath.Join(dir, "generic_struct.fern")
		if err := os.WriteFile(srcPath, []byte("struct Box[T] { v: T }\nfunction main(): i32 { var b: Box[i32] = Box { v: 3 }; return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a generic-struct program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-struct-field-ok", func(t *testing.T) {
		// #4346 piece 2 (generic-struct field slice): reading a type-parameter
		// field off a concrete instantiation now substitutes the arg, so
		// `Box[i32].v` is i32 and `return b.v` matches main's i32 return —
		// the program passes self-host `-check` (exit 0) where the pre-slice
		// self-host typed `b.v` as unknown and over-rejected.
		srcPath := filepath.Join(dir, "generic_struct_field.fern")
		if err := os.WriteFile(srcPath, []byte("struct Box[T] { v: T }\nfunction main(): i32 { var b: Box[i32] = Box { v: 3 }; return b.v; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a generic-struct field-access program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-nested-field-ok", func(t *testing.T) {
		// #4346 piece 2 (nested generic field spellings): a type-parameter that
		// appears INSIDE a field's spelling — `items: T[]`, `kv: (K, V)` — is now
		// substituted throughout, so off a `Wrapper[i32]` the field `items` is
		// i32[] (its element indexes to i32) and off a `Pair[i32, string]` the
		// field `kv` is the tuple (i32, string). The program reads both and
		// returns an i32, passing self-host `-check` (exit 0) where the pre-slice
		// self-host typed the nested field as unknown and over-rejected.
		srcPath := filepath.Join(dir, "generic_nested_field.fern")
		src := "struct Wrapper[T] { items: T[] }\n" +
			"struct Pair[K, V] { kv: (K, V) }\n" +
			"function main(): i32 {\n" +
			"    var w: Wrapper[i32] = Wrapper { items: [1, 2, 3] };\n" +
			"    var p: Pair[i32, string] = Pair { kv: (7, \"hi\") };\n" +
			"    return w.items[0] + p.kv.0;\n" +
			"}\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a nested generic field-access program exited %d, want 0", code)
		}
	})

	t.Run("check-generic-method-ret-ok", func(t *testing.T) {
		// #4346 piece 2 (generic-receiver method return): a method whose return
		// type names a receiver type parameter (`(b: Box[T]) get(): T`, nested
		// `(w: Wrapper[T]) all(): T[]`) is substituted with the receiver's
		// concrete instantiation args at the call site — so `Box[i32].get()` is
		// i32 (feeds main's i32 return) and `Wrapper[i32].all()` is i32[] (its
		// element indexes to i32). Both pass self-host `-check` (exit 0) where
		// the pre-slice self-host typed the call unknown and over-rejected.
		bare := filepath.Join(dir, "generic_method.fern")
		if err := os.WriteFile(bare, []byte("struct Box[T] { v: T }\nfunction (b: Box[T]) get(): T { return b.v; }\nfunction main(): i32 { var b: Box[i32] = Box { v: 5 }; return b.get(); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", bare); code != 0 {
			t.Errorf("-check on a generic-method-return program exited %d, want 0", code)
		}
		nested := filepath.Join(dir, "generic_method_nested.fern")
		if err := os.WriteFile(nested, []byte("struct Wrapper[T] { items: T[] }\nfunction (w: Wrapper[T]) all(): T[] { return w.items; }\nfunction main(): i32 { var w: Wrapper[i32] = Wrapper { items: [1, 2] }; return w.all()[0]; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", nested); code != 0 {
			t.Errorf("-check on a nested generic-method-return program exited %d, want 0", code)
		}
	})

	t.Run("check-dyn-bind-ok", func(t *testing.T) {
		// #4346 piece 2 (dyn Trait representation): a `dyn Trait` annotation now
		// resolves to a real TypeDyn instead of TypeUnknown, so binding a struct
		// value into a dyn slot (`var d: dyn Greet = Dog { }`) type-checks
		// (assignment into a dyn slot is lenient) — the program passes self-host
		// `-check` (exit 0) where the pre-slice self-host bound `d` to unknown
		// and silently over-rejected (the shape the over-reject test used to
		// pin). Method dispatch THROUGH the dyn value is the remaining unmodelled
		// shape — see check-overreject-not-silent below.
		srcPath := filepath.Join(dir, "dyn_bind.fern")
		src := "trait Greet { function hi(self: Self): i32; }\n" +
			"struct Dog { }\n" +
			"impl Greet for Dog { function hi(self: Self): i32 { return 7; } }\n" +
			"function main(): i32 { var d: dyn Greet = Dog { }; return 0; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if _, code := runDriver(t, "-check", srcPath); code != 0 {
			t.Errorf("-check on a dyn-binding program exited %d, want 0", code)
		}
	})

	t.Run("check-overreject-not-silent", func(t *testing.T) {
		// #4346: after the piece-2 slices the last unmodelled shape is method
		// DISPATCH through a dyn value — `d.hi()` where `d: dyn Greet`. The dyn
		// annotation now types (TypeDyn), so the binding is accepted (see
		// check-dyn-bind-ok), but a call on a dyn receiver isn't resolved to the
		// trait method, so `d.hi()` infers unknown and marks the module
		// ill-typed. No coded rule fires (the E043 method-existence pass has no
		// dyn-receiver arm, and Greet is object-safe so E021/E059/E060 stay
		// silent). This program is native-valid (exit 0) yet over-rejected here;
		// piece 1 surfaces a best-effort `error[type]` hint so the rejection is
		// never silent. The native `fern` stays the full oracle (this asserts
		// non-silence, not a code).
		srcPath := filepath.Join(dir, "overreject.fern")
		src := "trait Greet { function hi(self: Self): i32; }\n" +
			"struct Dog { }\n" +
			"impl Greet for Dog { function hi(self: Self): i32 { return 7; } }\n" +
			"function main(): i32 { var d: dyn Greet = Dog { }; return d.hi(); }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		_, code := runDriver(t, "-check", srcPath)
		if code != 1 {
			t.Errorf("-check on an over-rejected program exited %d, want 1", code)
		}
		if len(strings.TrimSpace(string(combined))) == 0 {
			t.Errorf("-check on an over-rejected program produced no diagnostic (silent exit 1) — #4346 fix regressed")
		}
		if !strings.Contains(string(combined), "error[type]") {
			t.Errorf("-check over-reject hint = %q, want it to contain \"error[type]\"", combined)
		}
	})

	t.Run("check-selfhost-no-e001", func(t *testing.T) {
		// The self-host modules are well-typed real code (they compile and
		// self-host), full of bare-identifier reads — locals, params, loop
		// variables, match payloads, lambda params, the module's own
		// functions / consts, enum/union variants, and the builtin Option /
		// Result / JsonValue / IoError variants. Running the self-host
		// `-check` over each (which flattens its `./` imports and checks the
		// whole bundle) must NOT report E001 (undefined name); any E001 here
		// is a false positive. This is the bundle-wide FP guard the
		// differential corpus can't express, mirroring the manual
		// "fern -check over every module" validation prior slices used.
		for _, m := range []string{"asmcore.fern", "lexer.fern", "parser.fern", "checker.fern", "flatten.fern", "interp.fern", "printer.fern", "astwalk.fern", "ssa.fern", "ssa_x86.fern", "ssa_arm64.fern", "ssa_wasm.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "arm64_native.fern", "x86_native.fern", "elf.fern", "fern.fern"} {
			combined, _ := exec.Command(fernBin, "-check", filepath.Join(dir, m)).CombinedOutput()
			if strings.Contains(string(combined), "error[E001]") {
				t.Errorf("-check on self-host module %s reported a spurious E001:\n%s", m, combined)
			}
		}
	})

	t.Run("check-position-undefined-assign", func(t *testing.T) {
		// E001 for an undefined assignment target is reported at the target
		// identifier (Go's errIdent position), not the `=` token.
		srcPath := filepath.Join(dir, "undef_assign.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { y = 5; return 0; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		combined, _ := exec.Command(fernBin, "-check", srcPath).CombinedOutput()
		if !strings.Contains(string(combined), "1:24: error[E001]") {
			t.Errorf("-check diagnostics = %q, want it to contain \"1:24: error[E001]\"", combined)
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

	t.Run("check-position-tuple-destructure", func(t *testing.T) {
		// E024 (destructuring a non-tuple) is reported at the destructure.
		for _, c := range []struct{ name, src, want string }{
			{"non_tuple", "function main(): i32 { var n = 5; var (a, b) = n; return a + b; }\n", "1:35: error[E024]"},
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

	t.Run("check-position-bad-receiver", func(t *testing.T) {
		// E021 (method receiver references an unknown type) is reported at
		// the method declaration.
		for _, c := range []struct{ name, src, want string }{
			{"unknown_receiver", "function (r: Nope) m(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", "1:1: error[E021]"},
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

	t.Run("check-position-missing-return", func(t *testing.T) {
		// E052 (non-void body can fall off the end) is reported at the
		// function declaration.
		for _, c := range []struct{ name, src, want string }{
			{"falls_off_end", "function f(): i32 { var x = 1; }\nfunction main(): i32 { return 0; }\n", "1:1: error[E052]"},
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

	t.Run("check-position-union-collision", func(t *testing.T) {
		// E016 (union alias collides with a struct) is reported at the
		// alias's `type` keyword.
		for _, c := range []struct{ name, src, want string }{
			{"name_collision", "struct A { x: i32 }\nstruct B { y: i32 }\nstruct C { z: i32 }\npub type B = A | C;\nfunction main(): i32 { return 0; }\n", "4:5: error[E016]"},
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

	t.Run("check-position-qualifier-mismatch", func(t *testing.T) {
		// E029 (variant pattern qualified by the wrong enum) is reported
		// at the arm.
		for _, c := range []struct{ name, src, want string }{
			{"wrong_qualifier", "enum E { A, B }\nenum F { C, D }\nfunction f(e: E): i32 { match (e) { F.A => { return 1; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A); }\n", "3:37: error[E029]"},
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

	t.Run("check-position-foreign-variant", func(t *testing.T) {
		// E014 (variant pattern not part of the scrutinee union) is
		// reported at the offending arm.
		for _, c := range []struct{ name, src, want string }{
			{"foreign_variant", "struct A { x: i32 }\nstruct B { y: i32 }\nstruct C { z: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, C(c) => { return c.z; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", "5:62: error[E014]"},
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

	t.Run("check-position-exhaustiveness", func(t *testing.T) {
		// E030 (non-exhaustive union match) is reported at the `match`
		// keyword.
		for _, c := range []struct{ name, src, want string }{
			{"missing_variant", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", "4:25: error[E030]"},
			{"enum_missing_variant", "enum E { A, B }\nfunction f(e: E): i32 { match (e) { A => { return 1; } } return 0; }\nfunction main(): i32 { return f(A); }\n", "2:25: error[E030]"},
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

	t.Run("check-position-struct-field-type", func(t *testing.T) {
		// E043 for a struct-literal field value whose type doesn't match
		// the declared field type is reported at the value.
		for _, c := range []struct{ name, src, want string }{
			{"string_for_i32", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p: P = P { x: 1, y: \"no\" }; return 0; }\n", "2:48: error[E043]"},
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

	t.Run("check-position-cast", func(t *testing.T) {
		// E033 (invalid cast) is reported at the cast's inner operand,
		// matching the Go checker's CastExpr position.
		for _, c := range []struct{ name, src, want string }{
			{"bool_to_i32", "function main(): i32 { var b: boolean = true; return b as i32; }\n", "1:56: error[E033]"},
			{"i32_to_bool", "function main(): i32 { var x: i32 = 1; var b: boolean = x as boolean; return 0; }\n", "1:59: error[E033]"},
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

	t.Run("check-position-field-assign", func(t *testing.T) { // E048 (assignment to an immutable field) is reported at the
		// field-access object, matching the Go checker.
		for _, c := range []struct{ name, src, want string }{
			{"field_assign", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 5; return p.x; }\n", "2:48: error[E048]"},
			{"field_compound", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x += 5; return p.x; }\n", "2:48: error[E048]"},
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

	t.Run("compile-rejects-immutable-mutation", func(t *testing.T) {
		// The COMPILE path (not just `-check`) must reject the immutable-data
		// cycle rules before codegen, so the self-host compiler is not MORE
		// permissive than native — which always type-checks ahead of codegen
		// (issue #2825: `p.x = v` must not compile+run on the self-host).
		// A rejection exits non-zero with the coded diagnostic on stderr and
		// emits NO asm; the sanctioned functional-update form still compiles.
		for _, c := range []struct {
			name   string
			src    string
			reject string // non-empty ⇒ expect rejection with this on stderr
		}{
			{"field-assign", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 9; return p.x; }\n", "error[E048]"},
			{"field-compound", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x += 5; return p.x; }\n", "error[E048]"},
			{"subscript-assign", "function main(): i32 { var a: i32[] = [1, 2, 3]; a[0] = 9; return a[0]; }\n", "error[E056]"},
			// Sanctioned replacements compile cleanly (no rejection).
			{"functional-update-ok", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p = P { ...p, x: 9 }; return p.x; }\n", ""},
			{"with-ok", "function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.with(0, 9); return a[0]; }\n", ""},
		} {
			sp := filepath.Join(dir, "compile_"+c.name+".fern")
			if err := os.WriteFile(sp, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write %s: %v", c.name, err)
			}
			cmd := exec.Command(fernBin, sp)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			_ = cmd.Run()
			code := cmd.ProcessState.ExitCode()
			if c.reject == "" {
				if code != 0 || stdout.Len() == 0 {
					t.Errorf("%s: valid program rejected (exit %d, %d bytes asm)\nstderr: %s", c.name, code, stdout.Len(), stderr.String())
				}
				continue
			}
			if code == 0 {
				t.Errorf("%s: forbidden mutation compiled (exit 0, %d bytes asm) — should be rejected", c.name, stdout.Len())
			}
			if !strings.Contains(stderr.String(), c.reject) {
				t.Errorf("%s: stderr = %q, want %q", c.name, stderr.String(), c.reject)
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
			{"and_nonbool", "function main(): i32 { var b: boolean = true; var x: boolean = b && 5; return 0; }\n", "1:66: error[E009]"},
			{"not_nonbool", "function main(): i32 { var x: boolean = !5; return 0; }\n", "1:41: error[E009]"},
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

	t.Run("fmt-enum", func(t *testing.T) {
		// An enum is desugared to one struct per variant before the printer
		// sees it, so a printer with no enum arm emits those structs and the
		// TYPE disappears — output that no longer compiles, since the
		// annotations naming it survive. -fmt rewrites source, so that is data
		// loss rather than a style difference (#6670).
		srcPath := filepath.Join(dir, "enum_fmt.fern")
		src := "@must_consume\nenum Shape[T] { Circle(i32), Blank }\n" +
			"function area(s: Shape): i32 { match (s) { Circle(r) => { return r * r; }, Blank => { return 0; } } return 0; }\n" +
			"function main(): i32 { return area(Circle(3)); }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out1, code := runDriver(t, "-fmt", srcPath)
		if code != 0 {
			t.Fatalf("-fmt exited %d, want 0", code)
		}
		got := string(out1)
		// The payload types, the generic parameter and the attribute all have
		// to come back, and no variant may surface as a free-standing struct.
		if !strings.Contains(got, "@must_consume\nenum Shape[T] { Circle(i32), Blank }") {
			t.Errorf("-fmt did not reconstruct the enum:\n%s", got)
		}
		if strings.Contains(got, "struct Circle") || strings.Contains(got, "struct Blank") {
			t.Errorf("-fmt emitted a variant as a bare struct:\n%s", got)
		}
		fmtPath := filepath.Join(dir, "enum_fmt_out.fern")
		if err := os.WriteFile(fmtPath, out1, 0o644); err != nil {
			t.Fatalf("write formatted: %v", err)
		}
		out2, code2 := runDriver(t, "-fmt", fmtPath)
		if code2 != 0 {
			t.Fatalf("second -fmt exited %d, want 0", code2)
		}
		if string(out1) != string(out2) {
			t.Errorf("-fmt is not idempotent on an enum:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
		}
		if _, checkCode := runDriver(t, "-check", fmtPath); checkCode != 0 {
			t.Errorf("formatted output failed -check (exit %d)", checkCode)
		}
	})

	t.Run("fmt-const", func(t *testing.T) {
		// A `const` desugars to a zero-arg FuncDecl, and print_module skips
		// decls with no source position so `@derive`'s synthesised methods stay
		// out of the output. The desugar left the position at zero, so the
		// declaration was dropped while every reference to it survived —
		// output naming an undefined symbol (#6688).
		//
		// Asserting the `const` SPELLING, not just that the name appears:
		// emitting the desugar as `function LIMIT(): i32 { … }` also makes the
		// name resolve and the self-host still compiles it, but native rejects
		// that form, so it is a silent rewrite into something only one compiler
		// accepts.
		srcPath := filepath.Join(dir, "const_fmt.fern")
		src := "const LIMIT: i32 = 41;\nfunction main(): i32 { return LIMIT + 1; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out1, code := runDriver(t, "-fmt", srcPath)
		if code != 0 {
			t.Fatalf("-fmt exited %d, want 0", code)
		}
		if !strings.Contains(string(out1), "const LIMIT: i32 = 41;") {
			t.Errorf("-fmt did not reconstruct the const:\n%s", out1)
		}
		if strings.Contains(string(out1), "function LIMIT") {
			t.Errorf("-fmt emitted the const as its desugared function:\n%s", out1)
		}
		fmtPath := filepath.Join(dir, "const_fmt_out.fern")
		if err := os.WriteFile(fmtPath, out1, 0o644); err != nil {
			t.Fatalf("write formatted: %v", err)
		}
		out2, code2 := runDriver(t, "-fmt", fmtPath)
		if code2 != 0 {
			t.Fatalf("second -fmt exited %d, want 0", code2)
		}
		if string(out1) != string(out2) {
			t.Errorf("-fmt is not idempotent on a const:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
		}
	})

	t.Run("emit-target-arm64", func(t *testing.T) {
		// The driver (x86-64 host binary) emits a runnable arm64 Linux ELF
		// directly under -target arm64-linux (in-process via arm64_native + elf.fern,
		// no `.s` + gcc/ld); run it under qemu and check the exit code.
		_, qemu := arm64Tooling(t) // skips if qemu absent
		srcPath := filepath.Join(dir, "arm_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		progBin := filepath.Join(dir, "arm_prog.bin")
		_, code := runDriver(t, "-target", "arm64-linux", "-o", progBin, srcPath)
		if code != 0 {
			t.Fatalf("-target arm64 emit exited %d, want 0", code)
		}
		raw, err := os.ReadFile(progBin)
		if err != nil {
			t.Fatalf("read emitted binary: %v", err)
		}
		f, err := elf.NewFile(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("-target arm64 output is not a parseable ELF: %v", err)
		}
		if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_EXEC {
			t.Fatalf("got machine=%v type=%v, want AARCH64/EXEC", f.Machine, f.Type)
		}
		if err := os.Chmod(progBin, 0o755); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		cmd := exec.Command(qemu, progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("arm64-emitted program exited %d, want 42", c)
		}
	})

	t.Run("emit-target-arm64-android", func(t *testing.T) {
		// -target arm64-android emits a static position-independent (ET_DYN)
		// arm64 ELF (in-process via arm64_native + elf.fern). The mmap'd low
		// heap lets it run at the kernel-chosen base; check ET_DYN + exit code
		// under qemu.
		_, qemu := arm64Tooling(t) // skips if qemu absent
		srcPath := filepath.Join(dir, "android_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { print(\"droid\"); return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		progBin := filepath.Join(dir, "android_prog.bin")
		_, code := runDriver(t, "-target", "arm64-android", "-o", progBin, srcPath)
		if code != 0 {
			t.Fatalf("-target arm64-android emit exited %d, want 0", code)
		}
		raw, err := os.ReadFile(progBin)
		if err != nil {
			t.Fatalf("read emitted binary: %v", err)
		}
		f, err := elf.NewFile(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("-target arm64-android output is not a parseable ELF: %v", err)
		}
		if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_DYN {
			t.Fatalf("got machine=%v type=%v, want AARCH64/DYN", f.Machine, f.Type)
		}
		if err := os.Chmod(progBin, 0o755); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		cmd := exec.Command(qemu, progBin)
		out, _ := cmd.CombinedOutput()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("arm64-android program exited %d, want 42 (out=%q)", c, out)
		}
	})

	t.Run("emit-target-wasm", func(t *testing.T) {
		// The driver emits a WASI WAT module under -target wasm32-wasi; run it
		// directly with wasmtime and check exit code + stdout.
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping -target wasm")
		}
		srcPath := filepath.Join(dir, "wasm_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { print(\"hi from wasm\"); return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "asm", srcPath)
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

	t.Run("emit-wasm-core-module", func(t *testing.T) {
		// The driver emits a wasm *binary* module under -target wasm32-wasi -emit core-module
		// (WAT -> watbin assembler -> .wasm). Validate with wasm-tools and
		// run it directly with wasmtime; check exit code + stdout.
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping -emit core-module")
		}
		srcPath := filepath.Join(dir, "wasmbin_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { print(\"hi from a core module\"); return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		// Binary bytes go to a file (-o), not stdout, so they survive
		// verbatim (incl. 0x00 / high bytes) without text mangling.
		outPath := filepath.Join(dir, "wasmbin_prog.wasm")
		// Still `-target wasm32-wasi -emit core-module`: this drives the SELF-HOSTED compiler,
		// which since #6635 shares cmd/fern's <isa>-<environment> target
		// vocabulary and `-emit` axis, and keeps its own `-ssa` flag. cmd/fern's move to
		// `-emit core-module` (#6536) does not reach it; converging the two
		// vocabularies is separate work.
		stdout, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "core-module", "-o", outPath, srcPath)
		if code != 0 {
			t.Fatalf("-emit core-module emit exited %d, want 0", code)
		}
		if len(stdout) != 0 {
			t.Errorf("-emit core-module with -o wrote %d bytes to stdout, want 0", len(stdout))
		}
		bin, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read core-module output: %v", err)
		}
		if len(bin) < 8 || bin[0] != 0x00 || bin[1] != 0x61 || bin[2] != 0x73 || bin[3] != 0x6d {
			t.Fatalf("output is not a wasm binary (magic missing): % x", bin[:min(8, len(bin))])
		}
		if wasmtools, err := exec.LookPath("wasm-tools"); err == nil {
			if out, err := exec.Command(wasmtools, "validate", outPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
			}
		}
		cmd := exec.Command(wasmtime, "run", outPath)
		out, _ := cmd.Output()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("core-module program exited %d, want 42", c)
		}
		if string(out) != "hi from a core module\n" {
			t.Errorf("core-module program stdout = %q, want %q", string(out), "hi from a core module\n")
		}
	})

	// watbin's opcode table is the only thing between the emitter's WAT and a
	// runnable `.wasm`, and a mnemonic missing from it takes down every program
	// that reaches the instruction — `f64.reinterpret_i64` blocked anything
	// calling `.to_string()` (#6607). These rows drive one program per opcode
	// family through `-emit core-module`, so a table gap fails here rather than
	// in whichever probe happens to need the instruction. The exit code is the
	// assertion: it comes from the program, so a wrong encoding that still
	// validates is caught too.
	t.Run("core-module-opcode-families", func(t *testing.T) {
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping core-module opcode families")
		}
		stdlibRoot, err := filepath.Abs("../../internal/stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		cases := []struct {
			name   string
			src    string
			exit   int
			stdout string
		}{
			// 0xbc-0xbf reinterprets + 0xb6 demote / 0xbb promote. The f32
			// forms ride a demote/promote pair, since an f32 travels in an
			// f64 slot on wasm.
			{"reinterpret-bits", `function main(): i32 {
    var x: f32 = 1.0 as f32;
    var b: i32 = f32_bits(x);
    var y: f32 = f32_from_bits(b);
    var d: i64 = f64_bits(2.0);
    var z: f64 = f64_from_bits(d);
    if (b == 1065353216 && y == x && z == 2.0 && d == 4611686018427387904) { return 42; }
    return 1;
}
`, 42, ""},
			// The #6607 repro: the decimal formatter reaches 0xbf, so a
			// program that merely prints a number needs it.
			{"to-string-prints", `import "std/i32";

function main(): i32 {
    print((41i32).to_string());
    return 0;
}
`, 0, "41\n"},
			// 0x67-0x69 / 0x79-0x7b — the i32 and i64 bit counts.
			{"bit-counts", `import "std/i32";
import "std/i64";

function main(): i32 {
    var a: i32 = (16i32).leading_zeros() + (16i32).trailing_zeros() + (255i32).count_ones();
    var b: i32 = (1099511627776i64).leading_zeros() + (1099511627776i64).trailing_zeros() + (255i64).count_ones();
    if (a == 39 && b == 71) { return 42; }
    return 1;
}
`, 42, ""},
			// 0xFC 10 `memory.copy` — `__memcpy`, reached by `string.bytes()`.
			{"bulk-memory-copy", `import "std/string";

function main(): i32 {
    var b: u8[] = "hello".bytes();
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < b.len()) { t = t + (b[i] as i32); i = i + 1; }
    if (t == 532 && b.len() == 5) { return 42; }
    return 1;
}
`, 42, ""},
			// 0xFC 11 `memory.fill` — `__memset`, core/map's bucket init.
			{"bulk-memory-fill", `function main(): i32 {
    var p: usize = __alloc(8);
    __memset(p, 65, 8);
    if ((__load_i32(p) & 255) == 65) { return 42; }
    return 1;
}
`, 42, ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srcPath := filepath.Join(dir, "wasmbin_"+tc.name+".fern")
				if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				outPath := filepath.Join(dir, "wasmbin_"+tc.name+".wasm")
				if _, code := runDriver(t, "-target", "wasm32-wasi", "-emit", "core-module", "-o", outPath, srcPath, stdlibRoot); code != 0 {
					t.Fatalf("-emit core-module emit exited %d, want 0", code)
				}
				cmd := exec.Command(wasmtime, "run", outPath)
				out, _ := cmd.Output()
				if c := cmd.ProcessState.ExitCode(); c != tc.exit {
					t.Errorf("exited %d, want %d", c, tc.exit)
				}
				if string(out) != tc.stdout {
					t.Errorf("stdout = %q, want %q", string(out), tc.stdout)
				}
			})
		}
	})

	t.Run("emit-wasm-component", func(t *testing.T) {
		// The driver emits a Component-Model wasi:cli/run component under
		// -target wasm32-wasi, picking the no-I/O or stdout framing from
		// the program's WASI usage. Validate with wasm-tools and run under
		// wasmtime.
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping the wasm component")
		}
		wasmtools, _ := exec.LookPath("wasm-tools")
		stdlibRoot, err := filepath.Abs("../../internal/stdlib")
		if err != nil {
			t.Fatalf("abs stdlib root: %v", err)
		}
		for _, c := range []struct {
			name    string
			src     string
			wantOut string
			// stdlib passes the stdlib root so a `core/*` import resolves to its
			// Fern source. Without it `map_new` still lowers — to the builtin
			// runtime helpers, which draw no hash seed — so the row would not
			// reach the import surface it is here to pin.
			stdlib bool
		}{
			{"no-io", "function main(): i32 { var s = 0; var i = 0; while (i < 7) { s = s + i; i = i + 1; } return s - 21; }\n", "", false},
			{"stdout", "function main(): i32 { print(\"hi from component\"); return 0; }\n", "hi from component\n", false},
			// core/map seeds its string hash from the same CSPRNG as random_i32,
			// so a Map pulls wasi:random/random's get-random-u64 into the core
			// with no random call in the program. The framing has to be chosen
			// accordingly: wrapped as plain stdout, this component carries a core
			// import no instantiation argument satisfies and will not load.
			{"map", "import \"core/map\";\nfunction main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert(\"a\", 1); if (!m.has(\"a\")) { return 1; } print(\"map ok\"); return 0; }\n", "map ok\n", true},
		} {
			srcPath := filepath.Join(dir, "comp_"+c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			outPath := filepath.Join(dir, "comp_"+c.name+".wasm")
			args := []string{"-target", "wasm32-wasi", "-o", outPath, srcPath}
			if c.stdlib {
				args = append(args, stdlibRoot)
			}
			stdout, code := runDriver(t, args...)
			if code != 0 {
				t.Fatalf("%s: the wasm component exited %d, want 0", c.name, code)
			}
			if len(stdout) != 0 {
				t.Errorf("%s: the component with -o wrote %d bytes to stdout, want 0", c.name, len(stdout))
			}
			bin, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("%s: read component: %v", c.name, err)
			}
			// Component preamble: "\0asm" + version 0x0d 0x00 + layer 0x01 0x00.
			if len(bin) < 8 || bin[0] != 0x00 || bin[1] != 0x61 || bin[2] != 0x73 || bin[3] != 0x6d || bin[6] != 0x01 {
				t.Fatalf("%s: output is not a wasm component: % x", c.name, bin[:min(8, len(bin))])
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", "--features", "component-model", outPath).CombinedOutput(); err != nil {
					t.Fatalf("%s: wasm-tools validate failed: %v\n%s", c.name, err, out)
				}
			}
			cmd := exec.Command(wasmtime, "run", outPath)
			out, _ := cmd.Output()
			if ec := cmd.ProcessState.ExitCode(); ec != 0 {
				t.Errorf("%s: component exited %d, want 0 (main returns 0)", c.name, ec)
			}
			if string(out) != c.wantOut {
				t.Errorf("%s: component stdout = %q, want %q", c.name, string(out), c.wantOut)
			}
		}
	})

	t.Run("emit-wasm-component-fs", func(t *testing.T) {
		// Filesystem wasi:cli/run components: read (component_full_io_fs),
		// write (component_full_io_fs_write), and read+write
		// (component_full_io_fs_rw). Run under wasmtime with a preopened dir.
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping the wasm component fs")
		}
		wasmtools, _ := exec.LookPath("wasm-tools")
		build := func(t *testing.T, name, src string) string {
			t.Helper()
			srcPath := filepath.Join(dir, "compfs_"+name+".fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			outPath := filepath.Join(dir, "compfs_"+name+".wasm")
			_, code := runDriver(t, "-target", "wasm32-wasi", "-o", outPath, srcPath)
			if code != 0 {
				t.Fatalf("%s: the wasm component exited %d, want 0", name, code)
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", "--features", "component-model", outPath).CombinedOutput(); err != nil {
					t.Fatalf("%s: wasm-tools validate failed: %v\n%s", name, err, out)
				}
			}
			return outPath
		}

		// read: print a preopened file's contents.
		fsDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(fsDir, "in.txt"), []byte("hello fs"), 0o644); err != nil {
			t.Fatalf("write in.txt: %v", err)
		}
		readBin := build(t, "read", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 1; } } }\n")
		out, _ := exec.Command(wasmtime, "run", "--dir", fsDir+"::/", readBin).Output()
		if string(out) != "hello fs" {
			t.Errorf("read: component stdout = %q, want %q", string(out), "hello fs")
		}

		// write: create a file, then verify its contents.
		writeBin := build(t, "write", "function main(): i32 { match (write_file(\"out.txt\", \"written\")) { Err(e) => { return 1; }, Ok(_) => {} } return 0; }\n")
		wDir := t.TempDir()
		if ec := exec.Command(wasmtime, "run", "--dir", wDir+"::/", writeBin).Run(); ec != nil {
			t.Fatalf("write: wasmtime run failed: %v", ec)
		}
		if got, err := os.ReadFile(filepath.Join(wDir, "out.txt")); err != nil || string(got) != "written" {
			t.Errorf("write: out.txt = %q (err %v), want %q", string(got), err, "written")
		}

		// read+write: copy in.txt -> out.txt.
		rwBin := build(t, "rw", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { match (write_file(\"out.txt\", s)) { Err(e) => { return 2; }, Ok(_) => {} } return 0; }, Err(e) => { return 1; } } }\n")
		rwDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(rwDir, "in.txt"), []byte("copy me"), 0o644); err != nil {
			t.Fatalf("write in.txt: %v", err)
		}
		if ec := exec.Command(wasmtime, "run", "--dir", rwDir+"::/", rwBin).Run(); ec != nil {
			t.Fatalf("rw: wasmtime run failed: %v", ec)
		}
		if got, err := os.ReadFile(filepath.Join(rwDir, "out.txt")); err != nil || string(got) != "copy me" {
			t.Errorf("rw: out.txt = %q (err %v), want %q", string(got), err, "copy me")
		}
	})

	t.Run("emit-wasm-component-wasi", func(t *testing.T) {
		// Single-category WASI wasi:cli/run shapes: env / args / clock /
		// random / stderr / exit. Each maps to its component_full_io_* wrap.
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping the wasm component wasi")
		}
		wasmtools, _ := exec.LookPath("wasm-tools")
		build := func(t *testing.T, name, src string) string {
			t.Helper()
			srcPath := filepath.Join(dir, "compw_"+name+".fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			outPath := filepath.Join(dir, "compw_"+name+".wasm")
			_, code := runDriver(t, "-target", "wasm32-wasi", "-o", outPath, srcPath)
			if code != 0 {
				t.Fatalf("%s: the wasm component exited %d, want 0", name, code)
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", "--features", "component-model", outPath).CombinedOutput(); err != nil {
					t.Fatalf("%s: wasm-tools validate failed: %v\n%s", name, err, out)
				}
			}
			return outPath
		}

		envBin := build(t, "env", "function main(): i32 { match (env(\"API_KEY\")) { Some(v) => { write(v); return 0; }, None => { write(\"MISS\"); return 1; } } }\n")
		if out, _ := exec.Command(wasmtime, "run", "--env", "API_KEY=sk-xyz", envBin).Output(); string(out) != "sk-xyz" {
			t.Errorf("env: stdout = %q, want %q", string(out), "sk-xyz")
		}

		argsBin := build(t, "args", "function main(): i32 { print_int(args().len()); return 0; }\n")
		if out, _ := exec.Command(wasmtime, "run", argsBin, "one", "two").Output(); string(out) != "3" {
			t.Errorf("args: stdout = %q, want %q (argv0 + 2)", string(out), "3")
		}

		clockBin := build(t, "clock", "function main(): i32 { var t: i64 = now_unix_ms(); if (t > 0) { write(\"ok\"); return 0; } return 1; }\n")
		if out, _ := exec.Command(wasmtime, "run", clockBin).Output(); string(out) != "ok" {
			t.Errorf("clock: stdout = %q, want %q", string(out), "ok")
		}

		monoBin := build(t, "mono", "function main(): i32 { var t: i64 = monotonic_ns(); if (t > 0) { write(\"ok\"); return 0; } return 1; }\n")
		if out, _ := exec.Command(wasmtime, "run", monoBin).Output(); string(out) != "ok" {
			t.Errorf("clock-mono: stdout = %q, want %q", string(out), "ok")
		}

		rndBin := build(t, "rnd", "function main(): i32 { var x: i32 = random_i32(); if (x == x) { write(\"ok\"); return 0; } return 1; }\n")
		if out, _ := exec.Command(wasmtime, "run", rndBin).Output(); string(out) != "ok" {
			t.Errorf("random: stdout = %q, want %q", string(out), "ok")
		}

		epBin := build(t, "eprint", "function main(): i32 { eprint(\"to stderr\"); return 0; }\n")
		{
			cmd := exec.Command(wasmtime, "run", epBin)
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			if !strings.Contains(eb.String(), "to stderr") {
				t.Errorf("eprint: stderr = %q, want it to contain %q", eb.String(), "to stderr")
			}
		}

		exitBin := build(t, "exit", "function main(): i32 { write(\"before\"); exit(0); return 0; }\n")
		{
			cmd := exec.Command(wasmtime, "run", exitBin)
			out, _ := cmd.Output()
			if string(out) != "before" {
				t.Errorf("exit: stdout = %q, want %q", string(out), "before")
			}
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("exit: exit code = %d, want 0", code)
			}
		}
	})

	t.Run("emit-wasm-component-combos", func(t *testing.T) {
		// Two-category fs-paired wasi:cli/run shapes: fs-read+env,
		// fs-rw+env, random+fs-write, fs-read+args, fs-rw+args.
		wasmtime, err := exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH; skipping the wasm component combos")
		}
		wasmtools, _ := exec.LookPath("wasm-tools")
		build := func(t *testing.T, name, src string) string {
			t.Helper()
			srcPath := filepath.Join(dir, "compc_"+name+".fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			outPath := filepath.Join(dir, "compc_"+name+".wasm")
			_, code := runDriver(t, "-target", "wasm32-wasi", "-o", outPath, srcPath)
			if code != 0 {
				t.Fatalf("%s: the wasm component exited %d, want 0", name, code)
			}
			if wasmtools != "" {
				if out, err := exec.Command(wasmtools, "validate", "--features", "component-model", outPath).CombinedOutput(); err != nil {
					t.Fatalf("%s: wasm-tools validate failed: %v\n%s", name, err, out)
				}
			}
			return outPath
		}
		mkdir := func(t *testing.T) string {
			d := t.TempDir()
			if err := os.WriteFile(filepath.Join(d, "in.txt"), []byte("DATA"), 0o644); err != nil {
				t.Fatalf("write in.txt: %v", err)
			}
			return d
		}

		// fs-read + env: print env value then file contents.
		readEnv := build(t, "read_env", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { match (env(\"P\")) { Some(v) => { write(v); write(s); return 0; }, None => { write(s); return 0; } } }, Err(e) => { return 1; } } }\n")
		d1 := mkdir(t)
		if out, _ := exec.Command(wasmtime, "run", "--dir", d1+"::/", "--env", "P=X-", readEnv).Output(); string(out) != "X-DATA" {
			t.Errorf("read+env: stdout = %q, want %q", string(out), "X-DATA")
		}

		// fs read+write + env: copy in.txt -> out.txt prefixed by env.
		rwEnv := build(t, "rw_env", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { match (env(\"P\")) { Some(v) => { match (write_file(\"out.txt\", v)) { Err(e) => { return 2; }, Ok(_) => {} } return 0; }, None => { return 3; } } }, Err(e) => { return 1; } } }\n")
		d2 := mkdir(t)
		if ec := exec.Command(wasmtime, "run", "--dir", d2+"::/", "--env", "P=ENV", rwEnv).Run(); ec != nil {
			t.Fatalf("rw+env: run failed: %v", ec)
		}
		if got, _ := os.ReadFile(filepath.Join(d2, "out.txt")); string(got) != "ENV" {
			t.Errorf("rw+env: out.txt = %q, want %q", string(got), "ENV")
		}

		// random + fs-write: draw a random i32, write a fixed file.
		rndWrite := build(t, "rnd_write", "function main(): i32 { var x: i32 = random_i32(); if (x != x) { return 9; } match (write_file(\"out.txt\", \"R\")) { Err(e) => { return 1; }, Ok(_) => {} } return 0; }\n")
		d3 := t.TempDir()
		if ec := exec.Command(wasmtime, "run", "--dir", d3+"::/", rndWrite).Run(); ec != nil {
			t.Fatalf("random+write: run failed: %v", ec)
		}
		if got, _ := os.ReadFile(filepath.Join(d3, "out.txt")); string(got) != "R" {
			t.Errorf("random+write: out.txt = %q, want %q", string(got), "R")
		}

		// fs-read + args: print arg count then file contents.
		readArgs := build(t, "read_args", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { print_int(args().len()); write(s); return 0; }, Err(e) => { return 1; } } }\n")
		d4 := mkdir(t)
		if out, _ := exec.Command(wasmtime, "run", "--dir", d4+"::/", readArgs, "a").Output(); string(out) != "2DATA" {
			t.Errorf("read+args: stdout = %q, want %q (argv0+1, then DATA)", string(out), "2DATA")
		}

		// fs read+write + args: copy in.txt -> out.txt, using args.
		rwArgs := build(t, "rw_args", "function main(): i32 { match (read_file(\"in.txt\")) { Ok(s) => { var n = args().len(); match (write_file(\"out.txt\", s)) { Err(e) => { return 2; }, Ok(_) => {} } return n - n; }, Err(e) => { return 1; } } }\n")
		d5 := mkdir(t)
		if ec := exec.Command(wasmtime, "run", "--dir", d5+"::/", rwArgs, "x", "y").Run(); ec != nil {
			t.Fatalf("rw+args: run failed: %v", ec)
		}
		if got, _ := os.ReadFile(filepath.Join(d5, "out.txt")); string(got) != "DATA" {
			t.Errorf("rw+args: out.txt = %q, want %q", string(got), "DATA")
		}
	})

	t.Run("wasm-component-rejects-unsupported", func(t *testing.T) {
		// A program mixing two WASI categories (env + args) has no single
		// wrap yet, so it must be rejected with a clear error rather than
		// emitting a broken component.
		srcPath := filepath.Join(dir, "comp_multi.fern")
		src := "function main(): i32 { var n = args().len(); match (env(\"X\")) { Some(v) => { return n; }, None => { return n + 1; } } }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "comp_multi.wasm"), srcPath)
		if code != 2 {
			t.Errorf("wasm-component on a multi-WASI program exited %d, want 2 (rejected)", code)
		}
	})

	// #6946: a builtin with no wasm meaning must be named by the diagnostic on
	// the CLI's wasm path too. The pre-emit gate lived only in the standalone
	// wasm drivers, which the CLI does not go through, so `-target wasm32-wasi`
	// carried the refusal all the way down to instruction selection: it named an
	// IR op nobody wrote (`syscall3`), and before that it emitted the op as a WAT
	// COMMENT, so the assembler blamed the comment marker ("unknown instruction:
	// ;;"). Both halves are asserted — the exit code and the builtin's name —
	// for one raw-floor intrinsic and for timer_fd, which had the same gap.
	t.Run("wasm-unsupported-builtin-names-the-builtin", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			src     string
			mustSay string
		}{
			{"syscall-floor", "function main(): i32 { return __syscall3(1, 1, 0, 0); }\n", "__syscall3"},
			{"raw-memory-floor", "function main(): i32 { var p: i32 = __raw_alloc(64); return 0; }\n", "__raw_alloc"},
			{"timer-fd", "function main(): i32 { return timer_fd(10); }\n", "timer_fd"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				srcPath := filepath.Join(dir, "unsupported_"+tc.name+".fern")
				if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
					t.Fatalf("write src: %v", err)
				}
				cmd := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "asm", srcPath)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				_ = cmd.Run()
				if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
					t.Fatalf("driver did not exit normally")
				}
				if code := cmd.ProcessState.ExitCode(); code != 1 {
					t.Errorf("exit = %d, want 1; stderr:\n%s", code, stderr.String())
				}
				if !strings.Contains(stderr.String(), tc.mustSay) || !strings.Contains(stderr.String(), "not supported on the wasm target") {
					t.Errorf("stderr does not name %s as unsupported on wasm:\n%s", tc.mustSay, stderr.String())
				}
				if strings.Contains(stdout.String(), "(module") {
					t.Errorf("refused but still emitted a module (%d bytes)", stdout.Len())
				}
			})
		}

		// The control: the same syscall program still compiles for a register
		// backend through this very CLI, which is what makes the wasm refusal a
		// backend limit rather than a rejected program.
		srcPath := filepath.Join(dir, "unsupported_syscall-floor.fern")
		out, code := runDriver(t, srcPath)
		if code != 0 {
			t.Fatalf("x86-64 refused a program it lowers: exit %d", code)
		}
		if !strings.Contains(string(out), ".globl") {
			t.Errorf("x86-64 emitted no assembly for the syscall program (%d bytes)", len(out))
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
