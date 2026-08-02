package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// Cross-validation across the three execution engines: the self-hosted
// tree-walking interpreter (interp.fern), the native (Go) tree-walking
// interpreter (internal/interp), and the self-hosted compiler's x86-64 output.
// Every source is piped through interp_run.fern and asm_run.fern and run
// directly against the native interp; all three must return the same exit code.
//
// This is the load-bearing parity net between the self-host compiler and the one
// implementation whose bugs are UNCORRELATED with it. The native interpreter is
// written in a different language and compiled by a different compiler, so it is
// the only engine here that cannot share a frontend bug with the others — see
// docs/NATIVE-CONVERGENCE.md §3, which keeps it permanently for exactly that
// reason. A self-host-only comparison would prove the compiler agrees with
// itself.
//
// SUBSET. This used to say "i32 only ... no arrays / strings / print* because
// those aren't all supported by the asm emitter today", naming asm.fern — an
// emitter deleted in #5972. asm_run now routes IR-or-error, and the IR path
// handles far more, so that restriction was long stale and was hiding real
// divergences behind an untested surface. The corpus below covers strings,
// arrays, structs and their methods, tuples, closures, higher-order functions,
// user enums with match, i64 and f64.
//
// Option / Result used to be listed here as a known gap: interp.fern mentioned
// none of Some / None / Ok / Err, so `Some(42)` failed at CONSTRUCTION and the
// driver exited 254 while native and both compiled backends answered correctly.
// That is closed (#5990) and the rows are below, which is the definition of
// done — a passing row, not a note.
//
// `to_ascii_lower` / `to_ascii_upper` were listed alongside it and do NOT
// belong here: they are `std/string` methods, not builtins, so `"A".to_ascii_
// lower()` is E043 on the native compiler too without `import "std/string"`.
// interp_run.fern runs `parser.parse_module` on raw stdin with no module
// loader, so NO stdlib import resolves in this suite regardless of engine.
// Covering them needs a driver that loads modules (the self-hosted CLI's
// `-interp`), not a row here.
//
// (A fourth engine — a bytecode VM, vm.fern — used to sit here too. It was
// retired in #4392: an unreachable fifth implementation of Fern semantics with
// no production consumer and known semantic drift from the other engines.)

func TestSelfHostCrossValidationX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	// Stage every lang source the two drivers transitively
	// depend on. (Each driver's modload pulls in only its own
	// imports, but using a shared dir means we can build both
	// side-by-side.)
	copySelfHostFiles(t, dir,
		"asmcore.fern", "lexer.fern", "parser.fern", "util.fern",
		"interp.fern", "astwalk.fern", "ir.fern", "irlower.fern", "asm_ir.fern",
		"interp_run.fern", "asm_run.fern")

	// Build both drivers through the shared cached path
	// (buildSelfHostBin), NOT a hand-rolled modload+emit+gcc: the
	// cached path releases the emit's dead spans back to the OS
	// (debug.FreeOSMemory) before spawning the assembler, and
	// restores a warm job's pre-linked binary from the disk cache
	// when one exists. The old inline build held the ~7 GB emit
	// residue in the test process while `as` spiked to ~8 GB on
	// asm_run's .s — over the 16 GB CI runners' RAM, so the kernel
	// OOM-killed the runner agent ("The runner has received a
	// shutdown signal", twice in a row on the same shard).
	interpBin := buildSelfHostBin(t, gcc, dir, "interp_run.fern", "interp_run.bin")
	asmBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "asm_run.bin")

	runDriver := func(t *testing.T, bin string, source string, captureStdout bool) (int, string) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(source))
		if captureStdout {
			out, _ := cmd.Output()
			return cmd.ProcessState.ExitCode(), string(out)
		}
		_, _ = cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), ""
	}

	// runAsm: pipe `source` to asm_run, capture the emitted
	// asm on its stdout, gcc-assemble it, run the inner
	// binary, return its exit code.
	runAsm := func(t *testing.T, source string) int {
		t.Helper()
		_, emittedAsm := runDriver(t, asmBin, source, true)
		caseDir := t.TempDir()
		innerAsm := filepath.Join(caseDir, "inner.s")
		innerBin := filepath.Join(caseDir, "inner")
		if err := os.WriteFile(innerAsm, []byte(emittedAsm), 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emittedAsm)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(runner[1:], innerBin)...)
		}
		_, _ = inner.CombinedOutput()
		return inner.ProcessState.ExitCode()
	}

	// runNativeInterp runs `source` against the native (Go) tree-walking
	// interpreter — the third leg, and the oracle, since it is the one engine
	// here whose bugs cannot correlate with the self-host frontend's.
	//
	// It now runs the SAME pipeline cmd/fern's `-interp` does, checker and
	// monomorph included. It used to skip the checker because three rows
	// returned a boolean from an `i32`-typed `main`; those rows are written
	// well-typed now (same operators, an `if` instead), which costs nothing and
	// buys every method-dispatch row in the corpus.
	//
	// Unlike the self-host mini-lexer/parser the other two legs
	// run on, the real Fern grammar has no implicit top-level
	// entry point — a source with no `function main` is a parse
	// error. Wrap bare-statement sources in a `main` so the same
	// test-case sources exercise all three legs.
	runNativeInterp := func(t *testing.T, source string) int {
		t.Helper()
		if !strings.Contains(source, "function main") {
			source = "function main(): i32 {\n" + source + "\n}\n"
		}
		prog, _, err := modload.LoadSource(source)
		if err != nil {
			t.Fatalf("native interp modload: %v\n--- source ---\n%s", err, source)
		}
		if err := constfold.Fold(prog); err != nil {
			t.Fatalf("native interp constfold: %v\n--- source ---\n%s", err, source)
		}
		// The full pipeline, matching cmd/fern's `-interp`: modload → constfold
		// → CHECK → MONOMORPH → interpret. The check and the monomorph pass used
		// to be skipped here, which quietly capped what this suite could cover:
		// method dispatch needs the checker's type info, so `s.len()` failed
		// inside the harness with "field access on non-struct interp.String"
		// even though `fern -interp` runs it fine. Every string / method /
		// generic row below was unreachable until this leg became the real
		// pipeline rather than a partial re-implementation of it.
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("native interp check: %v\n--- source ---\n%s", err, source)
		}
		if err := monomorph.Run(prog, info); err != nil {
			t.Fatalf("native interp monomorph: %v\n--- source ---\n%s", err, source)
		}
		ip := interp.New()
		for _, ed := range prog.Enums {
			ip.RegisterEnum(ed)
		}
		for _, fn := range prog.Funcs {
			ip.Register(fn)
		}
		v, err := ip.CallByName("main", nil)
		if err != nil {
			t.Fatalf("native interp run: %v\n--- source ---\n%s", err, source)
		}
		switch n := v.(type) {
		case interp.Number:
			return int(n) & 0xFF
		case interp.Bool:
			if n {
				return 1
			}
			return 0
		default:
			return 254
		}
	}

	cases := []struct {
		name   string
		source string
		want   int
	}{
		{"return-literal", "return 42;", 42},
		{"arithmetic", "return 1 + 2 * 3;", 7},
		{"parens", "return (1 + 2) * 3;", 9},
		{"subtraction", "return 100 - 23;", 77},
		{"division", "return 84 / 2;", 42},
		{"modulo", "return 23 % 5;", 3},
		{"unary-neg", "return 0 - 5 + 10;", 5},
		{"comparison-true", "if (5 < 10) { return 1; } return 0;", 1},
		{"comparison-false", "if (10 < 5) { return 1; } return 0;", 0},
		{"equality-true", "if (7 == 7) { return 1; } return 0;", 1},
		{"locals", "var x = 5; var y = 10; return x + y;", 15},
		{"reassign", "var x = 5; x = x + 3; return x;", 8},
		{"compound-assign", "var x = 1; x *= 6; x += 1; return x;", 7},
		{"if-then-branch", "var x = 5; if (x < 10) { return 1; } return 2;", 1},
		{"if-else-branch", "var x = 20; if (x < 10) { return 1; } return 2;", 2},
		{"while-sum", "var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15},
		{"while-early-return", "var i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 0 - 1;", 7},
		{"func-decl-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1},
		{
			"prime-count-up-to-30",
			"function is_prime(n: i32): i32 { if (n < 2) { return 0; } var i = 2; while (i * i <= n) { if (n % i == 0) { return 0; } i = i + 1; } return 1; } " +
				"function main(): i32 { var count = 0; var i = 2; while (i <= 30) { if (is_prime(i) == 1) { count += 1; } i = i + 1; } return count; }",
			10,
		},
		// Exit codes are clamped to 0..255 by Linux; these
		// expected values are the actual computation result MOD
		// 256.
		{
			"sum-of-squares-1-to-10",
			"function main(): i32 { var i = 1; var s = 0; while (i <= 10) { s += i * i; i += 1; } return s; }",
			385 % 256, // = 129
		},
		{
			"power-of-two-recursive",
			"function pow2(n: i32): i32 { if (n == 0) { return 1; } return 2 * pow2(n - 1); } " +
				"function main(): i32 { return pow2(10); }",
			1024 % 256, // = 0
		},
		// Struct-update `P { ...base, f: v }` — exercises the new
		// has_base path through all three engines (both interp
		// evaluators copy+override the base's fields; asm copies
		// non-overridden decl fields + stores overrides). i32-only
		// fields stay within the engines' common subset.
		{
			"struct-update-single-override",
			"struct P { x: i32, y: i32, z: i32 } function main(): i32 { var p: P = P { x: 1, y: 2, z: 3 }; var q: P = P { ...p, y: 20 }; return q.x + q.y + q.z; }",
			24,
		},
		{
			"struct-update-out-of-order",
			"struct P { x: i32, y: i32, z: i32 } function main(): i32 { var p: P = P { x: 1, y: 2, z: 3 }; var q: P = P { ...p, z: 7, x: 9 }; return q.x*100 + q.y*10 + q.z; }",
			927 % 256, // = 159
		},
		{
			"struct-update-in-return",
			"struct P { a: i32, b: i32 } function bump(p: P): P { return P { ...p, b: p.b + 100 }; } function main(): i32 { var p: P = P { a: 5, b: 6 }; var q: P = bump(p); return p.b*1000 + q.a*100 + q.b; }",
			6606 % 256, // = 206
		},
		// ---- beyond the old i32-only subset (see SUBSET above) ----------
		// Each row was verified to agree across native interp, self-host interp
		// and the compiled path before being added; the compiled leg was also
		// checked on wasm, which shares irlower with the x86 backend.
		{"string-len", `function main(): i32 { var s: string = "hello"; return s.len(); }`, 5},
		{"string-concat", `function main(): i32 { var a: string = "ab"; var b: string = "cde"; var c: string = a + b; return c.len(); }`, 5},
		{"string-index", `function main(): i32 { var s: string = "abc"; return s[1] as i32; }`, 98},
		{"string-slice", `function main(): i32 { var s: string = "abcdef"; var t: string = s[1:3] + ""; return t.len(); }`, 2},
		{"array-for-sum", `function main(): i32 { var xs: i32[] = [1,2,3,4]; var t = 0; for v in xs { t = t + v; } return t; }`, 10},
		{"string-array-for", `function main(): i32 { var xs: string[] = ["ab","cde"]; var t = 0; for s in xs { t = t + s.len(); } return t; }`, 5},
		{"struct-field-read", `struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 40, y: 2 }; return p.x + p.y; }`, 42},
		{"struct-method", `struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function main(): i32 { var p: P = P { x: 21 }; return p.dbl(); }`, 42},
		{"tuple-elements", `function main(): i32 { var t: (i32, i32) = (40, 2); return t.0 + t.1; }`, 42},
		{"closure-capture", `function main(): i32 { var n: i32 = 40; var f: () => i32 = function (): i32 { return n + 2; }; return f(); }`, 42},
		// A closure that WRITES its captured scalar. By-reference scalar capture
		// is a deliberate language feature, and the interpreter got it wrong for
		// months while the compiled path was correct (SH-057 / #2850) — exactly
		// the divergence class this suite exists to catch.
		{"closure-mutates-capture", `function main(): i32 { var n: i32 = 0; var inc: () => i32 = function (): i32 { n = n + 1; return n; }; inc(); inc(); return n + 40; }`, 42},
		{"higher-order-fn", `function ap(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { return ap(function (n: i32): i32 { return n + 1; }, 41); }`, 42},
		{"enum-match", `enum C { A, B } function main(): i32 { var c: C = C.A; match (c) { C.A => { return 3; }, C.B => { return 4; } } }`, 3},
		{"i64-arith", `function main(): i32 { var n: i64 = 5000000000; return (n % 97) as i32; }`, 73},
		{"f64-arith", `function main(): i32 { var f: f64 = 2.5; var g: f64 = 1.5; return (f + g) as i32; }`, 4},
		{"forward-declared-call", `function outer(n: i32): i32 { return inner(n) + 1; } function inner(n: i32): i32 { return n * 2; } function main(): i32 { return outer(20); }`, 41},
		// ---- Option / Result (#5990) ------------------------------------
		// The four builtin constructors are declared nowhere a program can
		// see, so each engine has to know them. interp.fern did not, and
		// every row below exited 254 there until #5990.
		{"option-some-match", `function main(): i32 { var o: Option[i32] = Some(42); match (o) { Some(v) => { return v; }, None => { return 0; } } }`, 42},
		{"option-none-match", `function main(): i32 { var o: Option[i32] = None; match (o) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
		{"option-string-payload", `function lookup(k: i32): Option[string] { if (k == 1) { return Some("one"); } return None; } function main(): i32 { match (lookup(1)) { Some(s) => { if (s == "one") { return 42; } return 3; }, None => { return 0; } } }`, 42},
		{"option-struct-payload", `struct P { x: i32 } function main(): i32 { var o: Option[P] = Some(P { x: 42 }); match (o) { Some(p) => { return p.x; }, None => { return 0; } } }`, 42},
		{"result-err-match", `function f(n: i32): Result[i32, string] { if (n < 0) { return Err("neg"); } return Ok(n * 2); } function main(): i32 { match (f(0 - 1)) { Ok(v) => { return v; }, Err(e) => { if (e == "neg") { return 42; } return 3; } } }`, 42},
		// `?` — the half that cannot be expressed as a value: on Err/None it
		// unwinds to the ENCLOSING function's return, so each engine needs a
		// notion of abrupt completion that stops at exactly one frame.
		{"try-propagates-err", `function f(n: i32): Result[i32, string] { if (n < 0) { return Err("neg"); } return Ok(n * 2); } function g(n: i32): Result[i32, string] { var v: i32 = f(n)?; return Ok(v + 1); } function main(): i32 { match (g(0 - 5)) { Ok(v) => { return v; }, Err(e) => { if (e == "neg") { return 42; } return 3; } } }`, 42},
		{"try-unwraps-some", `function h(o: Option[i32]): Option[i32] { var v: i32 = o?; return Some(v * 3); } function main(): i32 { match (h(Some(14))) { Some(v) => { return v; }, None => { return 0; } } }`, 42},
		{"try-propagates-none", `function h(o: Option[i32]): Option[i32] { var v: i32 = o?; return Some(v * 3); } function main(): i32 { match (h(None)) { Some(v) => { return v; }, None => { return 42; } } }`, 42},
		// A CLOSURE is a function boundary too, so the unwind stops at the
		// lambda. Found by this corpus: the native interpreter let the None
		// escape the lambda and exit the whole program 0, while both compiled
		// backends answered 42 — a divergence in the ORACLE leg, which is the
		// one this suite cannot catch by construction unless a row disagrees
		// with the other two. Also pinned as a four-backend fixture
		// (testdata/cases/try_op_in_closure).
		{"try-in-closure", `function main(): i32 { var f: (Option[i32]) => Option[i32] = function (o: Option[i32]): Option[i32] { var v: i32 = o?; return Some(v + 1); }; match (f(None)) { Some(a) => { return a; }, None => { match (f(Some(41))) { Some(b) => { return b; }, None => { return 0; } } } } }`, 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interpExit, _ := runDriver(t, interpBin, tc.source, false)
			nativeInterpExit := runNativeInterp(t, tc.source)
			asmExit := runAsm(t, tc.source)
			if interpExit != tc.want || nativeInterpExit != tc.want || asmExit != tc.want {
				t.Errorf("disagreement: interp=%d, native-interp=%d, asm=%d, want=%d\n--- source ---\n%s",
					interpExit, nativeInterpExit, asmExit, tc.want, tc.source)
			}
		})
	}
}
