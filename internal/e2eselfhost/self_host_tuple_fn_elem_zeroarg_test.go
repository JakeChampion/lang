package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A bare ZERO-ARG named function in an fn-typed tuple element SIGSEGV'd on the
// IR path while the interpreter returned the right answer (#5834 isolated it,
// this closes it).
//
// The lift boxes a fn-valued tuple element into a `__mkclo$…` env box so
// `t.N(args)` dispatches env-first — the "clo" element tag `tuple_type_elem_tag`
// derives from a declared "fn" segment assumes exactly that box. But it skipped
// a bare ident naming a ZERO-ARG function, deliberately: Fern desugars
// `const X = expr` into a zero-arg function, so a bare `X` in expression position
// must read as the const's VALUE, and boxing it would break every const read.
//
// The element's ANNOTATION resolves the ambiguity — `((() => i32), i32)` says
// element 0 is a fn value, so no const-call reading exists there. So the boxing
// now happens at the binding, where the declared type is in hand, and only for
// elements the annotation calls a fn. Unannotated tuples are untouched, which is
// what keeps const reads working.
//
// The split was ARITY, not nesting: a one-param named fn and a lambda were
// already boxed and already correct, in both the plain and array-nested forms.
// All four are pinned here so a future change cannot fix one and regress another.
//
// The Option / Result cases below are the same disagreement one container over —
// the variant-constructor walk skips a zero-arg payload for the same const
// reason, while the match-arm bind dispatches it env-first regardless — and take
// the same annotation-resolves-it fix. So do a fn-typed RETURN and a USER-enum
// variant field: four positions, one cause. The user-enum one needs no annotation
// at all, because the variant's struct decl already types the field "fn" — there
// only the arity gate was in the way.
var tupleFnZeroArgCases = []struct {
	name string
	src  string
	exit int
}{
	// The two shapes that crashed.
	{"tuple-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { var t: ((() => i32), i32) = (a1, 4); return t.0() + t.1; }", 7},
	{"tuple-in-array-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { var xs: ((() => i32), i32)[] = [(a1, 4)]; return xs[0].0() + xs[0].1; }", 7},
	// Both elements fn-typed — the walk must box each independently and hoist a
	// distinct wrapper per element.
	{"tuple-two-fn-elems", "function a1(): i32 { return 3; }\nfunction a2(): i32 { return 5; }\nfunction main(): i32 { var t: ((() => i32), (() => i32)) = (a1, a2); return t.0() * 10 + t.1(); }", 35},
	// Already-working shapes: a named fn WITH params, and a lambda. These were
	// boxed by the ordinary tuple walk before and must not be double-boxed now.
	{"tuple-onearg", "function a1(x: i32): i32 { return x + 3; }\nfunction main(): i32 { var t: (((i32) => i32), i32) = (a1, 4); return t.0(1) + t.1; }", 8},
	{"tuple-lambda", "function main(): i32 { var t: ((() => i32), i32) = (() => 3, 4); return t.0() + t.1; }", 7},

	// ---- the const-read regression cases ----
	// `const K` desugars to a zero-arg function, so these are exactly what an
	// over-eager version of this fix breaks: each must keep reading K as its
	// VALUE. A wrong implementation boxes K's address and these return garbage
	// or crash.
	{"const-in-tuple", "const K: i32 = 9;\nfunction main(): i32 { var t: (i32, i32) = (K, 4); return t.0 + t.1; }", 13},
	{"const-read-plain", "const K: i32 = 9;\nfunction main(): i32 { return K + 1; }", 10},
	// A const and a fn value in the SAME annotated tuple: element 0 is boxed,
	// element 1 must not be.
	{"const-and-fn-mixed", "const K: i32 = 9;\nfunction a1(): i32 { return 3; }\nfunction main(): i32 { var t: ((() => i32), i32) = (a1, K); return t.0() + t.1; }", 12},
	// UNANNOTATED tuple carrying a const — no declared fn segment, so the
	// pre-pass must not fire at all.
	{"unannotated-tuple-const", "const K: i32 = 9;\nfunction main(): i32 { var t = (K, 4); return t.0 + t.1; }", 13},

	// ---- the same disagreement one container over: Option / Result payloads ----
	// `Some(f)` / `Ok(f)` / `Err(f)` are boxed by the variant-constructor walk
	// when the payload has >= 1 param, and skipped when it is zero-arg, for the
	// same const reason — while the match-arm bind dispatches the payload
	// env-first regardless. Same annotation-resolves-it fix.
	{"option-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { var o: Option[() => i32] = Some(a1); match (o) { Some(f) => { return f(); }, None => { return 0; } } return 9; }", 3},
	{"result-ok-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { var r: Result[() => i32, string] = Ok(a1); match (r) { Ok(f) => { return f(); }, Err(e) => { return 0; } } return 9; }", 3},
	// Err takes Result's SECOND type arg — a fix that always indexes arg 0 passes
	// the two rows above and fails this one.
	{"result-err-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { var r: Result[i32, () => i32] = Err(a1); match (r) { Ok(v) => { return v; }, Err(f) => { return f(); } } return 9; }", 3},
	// Already-working payload shapes.
	{"option-onearg", "function a1(x: i32): i32 { return x + 3; }\nfunction main(): i32 { var o: Option[(i32) => i32] = Some(a1); match (o) { Some(f) => { return f(1); }, None => { return 0; } } return 9; }", 4},
	{"option-lambda", "function main(): i32 { var o: Option[() => i32] = Some(() => 3); match (o) { Some(f) => { return f(); }, None => { return 0; } } return 9; }", 3},
	// A fn-typed Option that is NONE — the pre-pass must not choke on a payloadless
	// initialiser.
	{"option-none", "function a1(): i32 { return 3; }\nfunction main(): i32 { var o: Option[() => i32] = None; match (o) { Some(f) => { return f(); }, None => { return 5; } } return 9; }", 5},
	// Const-read regressions for the variant pre-pass, mirroring the tuple ones.
	{"option-const-payload", "const K: i32 = 9;\nfunction main(): i32 { var o: Option[i32] = Some(K); match (o) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 9},
	{"result-const-payload", "const K: i32 = 9;\nfunction main(): i32 { var r: Result[i32, string] = Ok(K); match (r) { Ok(v) => { return v; }, Err(e) => { return 0; } } return 9; }", 9},
	{"option-unannotated-const", "const K: i32 = 9;\nfunction main(): i32 { var o = Some(K); match (o) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 9},

	// ---- two more positions with the same split ----
	// The declared RETURN type is the annotation: the caller binds the result as a
	// fn value and dispatches env-first, so a bare zero-arg name must be boxed.
	{"returned-zeroarg", "function a1(): i32 { return 3; }\nfunction get(): () => i32 { return a1; }\nfunction main(): i32 { var g: () => i32 = get(); return g(); }", 3},
	{"returned-onearg", "function a1(x: i32): i32 { return x + 3; }\nfunction get(): (i32) => i32 { return a1; }\nfunction main(): i32 { var g: (i32) => i32 = get(); return g(1); }", 4},
	{"returned-lambda", "function get(): () => i32 { return () => 3; }\nfunction main(): i32 { var g: () => i32 = get(); return g(); }", 3},
	// A USER-enum variant field declared "fn" needs no annotation at all — the
	// struct decl already says it, which is strictly better evidence. Only the
	// arity gate was blocking it.
	{"user-enum-zeroarg", "enum E { Wrap(() => i32), Nil }\nfunction a1(): i32 { return 3; }\nfunction main(): i32 { var e: E = Wrap(a1); match (e) { Wrap(f) => { return f(); }, Nil => { return 0; } } return 9; }", 3},
	{"user-enum-lambda", "enum E { Wrap(() => i32), Nil }\nfunction main(): i32 { var e: E = Wrap(() => 3); match (e) { Wrap(f) => { return f(); }, Nil => { return 0; } } return 9; }", 3},
	// Const-read regressions for those two positions.
	{"const-return", "const K: i32 = 9;\nfunction get(): i32 { return K; }\nfunction main(): i32 { return get() + 1; }", 10},
	{"const-enum-payload", "enum E { Wrap(i32), Nil }\nconst K: i32 = 9;\nfunction main(): i32 { var e: E = Wrap(K); match (e) { Wrap(v) => { return v; }, Nil => { return 0; } } return 9; }", 9},
}

func TestSelfHostTupleFnZeroArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleFnZeroArgCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostTupleFnZeroArgIRWasm — the wasm leg. The fix is in the shared
// lift, so both backends pick it up; this pins that.
func TestSelfHostTupleFnZeroArgIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the zero-arg tuple-fn-element wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupleFnZeroArgCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "tuple_fn_zeroarg.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s = %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
