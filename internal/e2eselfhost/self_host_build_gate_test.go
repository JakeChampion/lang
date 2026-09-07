package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostBuildGateX86_64 pins the invariant #6961 restored: the self-host
// CLI's COMPILE path rejects a program its own checker rejects. Before it the
// gate ran over six codes, so `-check` reported E003 on a source that
// `-target` then compiled to a working binary, and every checker rule ported
// for parity stayed reachable only through `-check`.
//
// Both directions are the test. The gate is an exclusion list
// (`is_partial_checker_gap_code` in checker.fern), so a case either names a
// code that must now reject the build, or one of the partial-port rules that
// must NOT — a valid program drawing one of those still has to compile, which
// is the failure mode that kept the gate at six codes in the first place. A
// change that widens the exclusion list silently is what the second group
// catches; one that narrows it too far is what the first group catches.
func TestSelfHostBuildGateX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := []struct {
		name string
		src  string
		// wantDiag non-empty ⇒ the compile must fail with it on stderr.
		// Empty ⇒ the compile must succeed, whatever `-check` says.
		wantDiag string
	}{
		{
			// The issue's own repro: assignment type error, reported by both
			// checkers, compiled clean by the self-host until the gate widened.
			name:     "assign-mismatch-E003",
			src:      "function main(): i32 { var s: string = 5; return 0; }\n",
			wantDiag: "error[E003]",
		},
		{
			name:     "wildcard-arm-not-last-E026",
			src:      "enum O { Sm(i32), Nn }\nfunction main(): i32 { var o: O = O.Nn; match (o) { _ => { return 1; }, Nn => { return 3; } } }\n",
			wantDiag: "error[E026]",
		},
		{
			name:     "variant-covered-twice-E028",
			src:      "enum O { Sm(i32), Nn }\nfunction main(): i32 { var o: O = O.Sm(1); match (o) { Sm(a) => { return a; }, Sm(b) => { return b; }, Nn => { return 3; } } }\n",
			wantDiag: "error[E028]",
		},
		{
			// One of the six codes that gated before this change, so the
			// widening cannot be read as having replaced the old set.
			name:     "field-assign-E048",
			src:      "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 5; return p.x; }\n",
			wantDiag: "error[E048]",
		},
		{
			// #7273: an under-supplied call. Both oracles report E004; the
			// self-host checker did too, and only `-check` ever saw it — the
			// build emitted a binary that read the missing argument out of
			// whatever was in the register.
			name:     "call-too-few-args-E004",
			src:      "function two(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return two(1); }\n",
			wantDiag: "error[E004]",
		},
		{
			// The shape that kept E004 off the gate: a FUNCTION-TYPED parameter
			// shadowing a module-level function of the same name. The call
			// `f(v)` is a closure call, but the arity branch was guarded on
			// "the name has no known type" rather than "the name is not bound",
			// and a fn-typed parameter has no known type here — so it compared
			// the call against the module-level `f` and reported a bogus E004.
			// Native accepts this program; it must still build.
			name:     "fn-typed-param-shadows-module-fn-compiles",
			src:      "function apply(f: (i32) => i32, v: i32): i32 { return f(v); }\nfunction f(): i32 { return 1; }\nfunction main(): i32 { return apply((x: i32) => x + 1, 1) + f(); }\n",
			wantDiag: "",
		},
		{
			// #7311: an array builtin called with the wrong argument count.
			// `len` / `append` / `with` are the three unconditional array
			// builtins and their arity is fixed by the language, so this is a
			// plain mistake — but it used to reach lowering, which refuses the
			// whole module as "not IR-eligible", naming neither the call nor
			// the mistake.
			name:     "array-builtin-arity-E004",
			src:      "function main(): i32 { var a: i32[] = [1, 2]; return a.len(3); }\n",
			wantDiag: "error[E004]",
		},
		{
			// The negative control for the row above: the same three builtins
			// called correctly must still compile. A wrong arity constant here
			// would reject real programs rather than merely mis-report them,
			// because E004 gates the build (#7273).
			name:     "array-builtins-at-correct-arity-compile",
			src:      "function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = a.append(3); var c: i32[] = b.with(0, 9); return c.len(); }\n",
			wantDiag: "",
		},
		{
			// #7447: a function named with a reserved keyword. The permissive
			// parser leaves an empty name and consumes nothing, so the body
			// parsed on as top-level statements and the CHECKER reported E052
			// plus an E001 per parameter — every diagnostic pointing away from
			// the cause, and none of them gating the build. asmcore's
			// check_decl_names already had the right message; it was wired only
			// into two wasm drivers, never into the compiler. Native reports
			// P001 here, so this is code-set parity as well as a better message.
			name:     "keyword-fn-name-P001",
			src:      "struct B { items: i32[] }\nfunction use(own p: B): i32 { return p.items.len(); }\nfunction main(): i32 { var a: B = B { items: [] }; return use(a); }\n",
			wantDiag: "error[P001]",
		},
		{
			// The sibling the same gate already carried: a struct named with a
			// keyword. Unreachable from the compiler until the gate was wired in.
			name:     "keyword-struct-name-P001",
			src:      "struct use { x: i32 }\nfunction main(): i32 { return 0; }\n",
			wantDiag: "error[P001]",
		},
		{
			// The negative control, and the one that matters: renaming the
			// function is all it takes, so the gate must fire on the NAME and
			// nothing else. 320 sources (the whole stdlib, every self-host
			// module, the fixtures) were scanned for a false positive here.
			name:     "non-keyword-fn-name-compiles",
			src:      "function consume(n: i32): i32 { return n + 1; }\nfunction main(): i32 { return consume(1); }\n",
			wantDiag: "",
		},
		{
			// #7311's remaining half: the STRING builtins and the free
			// builtins had no arity rule either — `s.len(1)` and
			// `print("a", "b")` reached lowering and were refused as "not
			// IR-eligible", naming neither the call nor the mistake. Native
			// reports E004 at the call; now the self-host does too, and the
			// gate makes it a build rejection.
			name:     "string-builtin-arity-E004",
			src:      "function main(): i32 { var s: string = \"abc\"; return s.len(1); }\n",
			wantDiag: "error[E004]",
		},
		{
			name:     "free-builtin-arity-E004",
			src:      "function main(): i32 { print(\"a\", \"b\"); return 0; }\n",
			wantDiag: "error[E004]",
		},
		{
			// #7311's last half: a call through a name BOUND IN SCOPE. The
			// free-function arity rule is skipped for such a name, so nothing
			// checked a closure call at all — this built clean and the callee
			// read its second argument out of whatever was in the register
			// (the binary returned 121 rather than failing).
			name:     "closure-call-arity-E004",
			src:      "function main(): i32 { var g = (a: i32, b: i32): i32 => { return a + b; }; return g(1); }\n",
			wantDiag: "error[E004]",
		},
		{
			// The same hole reached through a named function used as a VALUE
			// rather than a lambda: `g` is a plain local, so the callee it
			// resolves to was never consulted for arity.
			name:     "fn-value-call-arity-E004",
			src:      "function dbl(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { var g = dbl; return g(1); }\n",
			wantDiag: "error[E004]",
		},
		{
			// And through a fn-typed PARAMETER, whose TypeFunc comes from the
			// declaration's sidecars rather than a lambda.
			name:     "fn-typed-param-call-arity-E004",
			src:      "function ap(f: (i32, i32) => i32): i32 { return f(1); }\nfunction main(): i32 { return ap((a: i32, b: i32): i32 => a + b); }\n",
			wantDiag: "error[E004]",
		},
		{
			// Negative control: the same three callable shapes at the correct
			// arity, including a zero-parameter callable — `()` must read as
			// arity 0 and not as "no parameter list recorded". Runs to 10.
			name:     "closure-and-fn-value-at-correct-arity-compile",
			src:      "function dbl(a: i32, b: i32): i32 { return a + b; }\nfunction z(f: () => i32): i32 { var q = f; return q(); }\nfunction main(): i32 { var g = (a: i32, b: i32): i32 => { return a + b; }; var h = dbl; return g(1, 2) + h(3, 4) + z((): i32 => 0); }\n",
			wantDiag: "",
		},
		{
			// The load-bearing negative control. A "fn"-coarsened struct FIELD
			// carries no param spellings, so its TypeFunc's empty param list is
			// unrecorded rather than arity 0 (TypeFunc.params_known). Without
			// that distinction the rule above reads it as arity 0 and rejects
			// this program — which native accepts — with a false
			// "expects 0 arguments, got 1".
			//
			// This pins the checker's silence, not the binary: the self-host
			// MISCOMPILES the rebind (#7862) — it calls the closure box address
			// instead of the code pointer in its slot 0, and the binary
			// segfaults. Native and the interpreter both answer 3. That defect
			// predates this rule and is unrelated to arity.
			name:     "fn-typed-struct-field-rebind-has-no-arity-to-check",
			src:      "struct H { f: (i32) => i32 }\nfunction apply_h(h: H): i32 { var g = h.f; return g(2); }\nfunction inc(x: i32): i32 { return x + 1; }\nfunction main(): i32 { return apply_h(H { f: inc }); }\n",
			wantDiag: "",
		},
		{
			// Negative control: the same builtins at the correct arity, in a
			// program whose statements all now carry types (print is void,
			// as_bytes is u8[]) — a wrong arity constant or type arm here
			// would reject real programs, because E004 gates the build.
			name:     "string-and-free-builtins-at-correct-arity-compile",
			src:      "function main(): i32 { print(\"a\"); var s: string = \"abc\"; return s.len() + s.as_bytes().len(); }\n",
			wantDiag: "",
		},
		{
			// A valid i64 program compiles. This drew a spurious E043 when the
			// checker ignored integer width; #7011 closed that, and both
			// checkers are now silent here. The case stays as the regression
			// guard for the width rule.
			name:     "i64-program-compiles",
			src:      "import \"std/i64\";\nfunction main(): i32 { var a: i64 = 9i64; var b: i64 = 3i64; return (a / b) as i32; }\n",
			wantDiag: "",
		},
		{
			// #7380: a stdlib method called without the import that makes it
			// visible. There is no prelude injector (docs/PRELUDE-TO-MODULES.md)
			// — a program sees only what it imports — and native says so with
			// E043 naming the import to add. The self-host checker said it too;
			// only the gate did not, so the missing import compiled into a
			// working binary and the mistake surfaced somewhere else entirely.
			// One case per receiver kind, because the three take different
			// resolution arms: scalar, array, string.
			name:     "unimported-i32-method-E043",
			src:      "function main(): i32 { var t: string = 7.to_string(); return t.len(); }\n",
			wantDiag: "error[E043]",
		},
		{
			name:     "unimported-array-method-E043",
			src:      "function main(): i32 { var xs: string[] = [\"ab\", \"cd\"]; var t: string = xs.join(\",\"); return t.len(); }\n",
			wantDiag: "error[E043]",
		},
		{
			name:     "unimported-string-method-E043",
			src:      "function main(): i32 { var s: string = \"aXb\"; var t: string = s.replace(\"X\", \"Y\"); return t.len(); }\n",
			wantDiag: "error[E043]",
		},
		{
			// The negative control for the three rows above, and the reason
			// they are not a spelling check: with the import the same calls are
			// valid, so a rule that rejected on the method NAME rather than on
			// what is in scope would fail here.
			name:     "imported-stdlib-methods-compile",
			src:      "import \"std/i32\";\nimport \"std/array\";\nimport \"std/string\";\nfunction main(): i32 { var t: string = 7.to_string(); var xs: string[] = [\"ab\", \"cd\"]; var j: string = xs.join(\",\"); var r: string = \"aXb\".replace(\"X\", \"Y\"); return t.len() + j.len() + r.len(); }\n",
			wantDiag: "",
		},
		{
			// A user type sharing a name with a stdlib generic's type PARAMETER.
			// Modules are merged into one program before checking, so `struct T`
			// otherwise captured every `[T]` in the stdlib and its bodies read
			// `a.cmp(b)` as a field of that struct — 46 spurious E043s on this
			// program alone, which is what kept E043 off the gate. Native scopes
			// the lookup (#6118); the self-host now erases a function's own type
			// parameters when it resolves a spelling.
			name:     "type-param-name-collision-compiles",
			src:      "import \"std/array\";\nstruct T { z: i32 }\nstruct K { a: i32 }\nfunction main(): i32 { var xs: i32[] = [1, 2, 3]; var t: T = T { z: xs.sum() }; var k: K = K { a: xs.len() }; return t.z + k.a; }\n",
			wantDiag: "",
		},
		{
			// E064 gates now (#8461), so this is a negative control rather than
			// an exclusion: the checker registers the front end's built-in
			// struct declarations before it types anything, the way native's
			// builtinStructDecls does, so a valid stdlib import draws no
			// "unknown type" at all. Before that, `import "std/platform"` drew
			// 7 E021 + 2 E064 and `import "std/time"` 18 + 24, on programs
			// whose only content was the import.
			name:     "unknown-stdlib-type-not-reported-E064",
			src:      "import \"std/io\";\nfunction main(): i32 { var r: i32 = 0; return r; }\n",
			wantDiag: "",
		},
		{
			name:     "builtin-struct-decls-registered-time",
			src:      "import \"std/time\";\nfunction main(): i32 { return 0; }\n",
			wantDiag: "",
		},
		{
			name:     "builtin-struct-decls-registered-platform",
			src:      "import \"std/platform\";\nfunction main(): i32 { return 0; }\n",
			wantDiag: "",
		},
		{
			// #8461's headline. E041 exists because array `==` is not
			// structural equality; exempted from the gate, the comparison
			// lowered to a POINTER compare and two equal arrays reported
			// unequal — a plausible wrong answer where both checkers knew the
			// error. `-check` reported it the whole time.
			name:     "array-equality-E041",
			src:      "function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys: i32[] = [1, 2, 3]; if (xs == ys) { return 1; } return 0; }\n",
			wantDiag: "error[E041]",
		},
		{
			// The negative control: `==` on the ELEMENTS is what the E041
			// message tells the author to write, so it must build.
			name:     "array-element-equality-compiles",
			src:      "function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys: i32[] = [1, 2, 3]; if (xs[0] == ys[0]) { return 1; } return 0; }\n",
			wantDiag: "",
		},
		{
			// E001 through the discard. `_` introduces no name, so reading it
			// back is an undefined identifier — native says so, this checker
			// said so, and only the gate did not: the binary returned 2, the
			// element the binding said to throw away
			// (conformance/cases/underscore_not_readable).
			name:     "discard-read-back-E001",
			src:      "function pair(): (i32, i32) { return (1, 2); }\nfunction main(): i32 { var (a, _) = pair(); return _; }\n",
			wantDiag: "error[E001]",
		},
		{
			// E051: the `own`-param discipline. Passing the same value twice
			// hands the callee a pointer it already consumed.
			name:     "owned-arg-used-twice-E051",
			src:      "struct B { items: i32[] }\nfunction consume(own p: B): i32 { return p.items.len(); }\nfunction main(): i32 { var a: B = B { items: [1] }; var n: i32 = consume(a); return n + consume(a); }\n",
			wantDiag: "error[E051]",
		},
		{
			// E052: a value-returning function that can fall off its end.
			name:     "missing-return-E052",
			src:      "function f(n: i32): i32 { if (n > 0) { return 1; } }\nfunction main(): i32 { return f(1); }\n",
			wantDiag: "error[E052]",
		},
		{
			// The E052 negative control that the gate turned live: a
			// tuple / struct / literal match is REPLACED at parse time by a
			// done-flag if/else chain, which falls through by construction, so
			// reading the chain says every such function can fall off its end.
			// The `if` carries the arms as written; block_exits reads those.
			name:     "tuple-match-exhausts-so-no-E052",
			src:      "function f(t: (i32, i32)): i32 { match (t) { (0, b) => { return b; }, (a, _) => { return a; } } }\nfunction main(): i32 { return f((0, 7)); }\n",
			wantDiag: "",
		},
		{
			// E013 / E018 are the two codes still exempt, and #8852 owns them:
			// the self-host parser never renames `_`, so a repeated discard
			// reads as a redeclared name. Native accepts this program; the
			// self-host CHECKER rejects it, so the gate has to keep letting it
			// build until the rename lands. When #8852 closes, this row's
			// wantDiag stays "" — the checker will simply stop reporting.
			name:     "repeated-discard-still-compiles-8852",
			src:      "function constant(_: i32, _: string): i32 { return 7; }\nfunction main(): i32 { var _ = 99; var _ = 98; return constant(1, \"a\"); }\n",
			wantDiag: "",
		},
		{
			// Negative controls for the five false positives #8461 had to fix
			// before the codes above could gate. Each is a program native
			// accepts that the self-host checker refused, so each would now be
			// a REJECTED BUILD rather than a stray diagnostic.
			//
			// A nested position in a `for` pattern: the binder encoding carries
			// it as a parenthesised group, and splitting on every comma bound
			// "(a" and "b)" — E001 for both `a` and `b`.
			name:     "for-nested-tuple-pattern-compiles",
			src:      "function main(): i32 { var deep: ((i32, i32), string)[] = [((2, 3), \"xy\")]; var s: i32 = 0; for ((a, b), c) in deep { s = s + a * b + c.len(); } return s; }\n",
			wantDiag: "",
		},
		{
			// The `@` whole-value binder names the scrutinee; nothing bound it.
			name:     "at-binder-compiles",
			src:      "enum One { Only(string) }\nfunction whole(o: One): i32 { return 1; }\nfunction main(): i32 { var o: One = Only(\"e\"); match (o) { w @ Only(v) => { return whole(w) + v.len(); } } }\n",
			wantDiag: "",
		},
		{
			// An if-EXPRESSION is an IIFE here and has no counterpart in the
			// native AST, so the erased `T` operands read as closure captures
			// with no runtime representation — E044 on a program native
			// accepts.
			name:     "if-expr-in-generic-compiles",
			src:      "function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }\nfunction main(): i32 { return pick(true, 1, 2); }\n",
			wantDiag: "",
		},
		{
			// An associated function reached through its ENUM: the qualified
			// -variant arm claimed the call first and reported a variant
			// nobody wrote (E036).
			name:     "enum-associated-fn-compiles",
			src:      "trait Empty { function empty(): Self; }\nenum Opt { Nothing, Just(i32) }\nimpl Empty for Opt { function empty(): Self { return Nothing; } }\nfunction main(): i32 { var o: Opt = Opt.empty(); match (o) { Nothing => { return 0; }, Just(n) => { return n; } } }\n",
			wantDiag: "",
		},
		{
			// An associated function a @derive supplies. A derive synthesises
			// no impl-table entry and `from_json` is not a requirement of the
			// `Json` trait either, so `User.from_json` resolved to nothing and
			// its own type name drew E001.
			name:     "derived-associated-fn-compiles",
			src:      "import \"std/json\";\n@derive(json.Json) struct User { id: i32, name: string }\nfunction main(): i32 { match (User.from_json(\"{\\\"id\\\":7,\\\"name\\\":\\\"g\\\"}\")) { Ok(u) => { return u.id; }, Err(e) => { return 1; } } }\n",
			wantDiag: "",
		},
		{
			// The uncoded #4346 hint (`error[type]`) is a statement about what
			// this checker can model, not about the program, so it must never
			// reject a build. `is_diagnostic_code` is what keeps it out.
			name:     "uncoded-partial-checker-hint-does-not-gate",
			src:      "enum W { Wrap(i32), Er2 }\nfunction main(): i32 { var w: W = W.Er2; match (w) { Wrap(Er2) => { return 1; }, Er2 => { return 2; } } }\n",
			wantDiag: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			progDir := t.TempDir()
			prog := filepath.Join(progDir, "prog.fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}
			out := filepath.Join(progDir, "prog.bin")
			cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", out, prog, stdlibRoot)
			stderr, _ := cmd.CombinedOutput()
			code := cmd.ProcessState.ExitCode()

			if tc.wantDiag == "" {
				if code != 0 {
					t.Fatalf("valid program rejected by the build gate: exit=%d\n%s", code, stderr)
				}
				return
			}
			if code == 0 {
				t.Fatalf("ill-typed program compiled clean (exit 0); wanted %s.\n"+
					"The compile path is not gating on this code — see #6961.", tc.wantDiag)
			}
			if !strings.Contains(string(stderr), tc.wantDiag) {
				t.Errorf("exit=%d but stderr missing %q\ngot: %s", code, tc.wantDiag, stderr)
			}
		})
	}
}

// TestSelfHostBuildGateMatchesCheckX86_64 states the property behind the case
// list above: for any program, if `-check` reports a gating code then
// `-target` must refuse to build it. #6961 was exactly this property failing —
// the two modes ran the same checker and disagreed about what it meant.
func TestSelfHostBuildGateMatchesCheckX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// Each source draws a gating diagnostic under `-check`. The property is
	// that the compile path agrees; the codes themselves are pinned above.
	//
	// Every source here must be one the GATE stops. An undefined call, say,
	// would pass this test without the gate existing at all — IR lowering
	// rejects it on its own — so it would prove nothing.
	srcs := []string{
		"function main(): i32 { var s: string = 5; return 0; }\n",
		"enum O { Sm(i32), Nn }\nfunction main(): i32 { var o: O = O.Sm(1); match (o) { Sm(a) => { return a; }, Sm(b) => { return b; }, Nn => { return 3; } } }\n",
		"struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 5; return p.x; }\n",
		"enum O { Sm(i32), Nn }\nfunction main(): i32 { var o: O = O.Nn; match (o) { _ => { return 1; }, Nn => { return 3; } } }\n",
		// #7273: this source is why the property matters — `-check` reported
		// E004 and `-target` built it anyway, for as long as E004 sat on the
		// exclusion list. IR lowering does not stop it either: the call is
		// well-formed, it is simply short an argument.
		"function two(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return two(1); }\n",
	}
	for i, src := range srcs {
		progDir := t.TempDir()
		prog := filepath.Join(progDir, "prog.fern")
		if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
			t.Fatalf("write prog: %v", err)
		}
		checkCmd := exec.Command(fernBin, "-check", prog, stdlibRoot)
		checkOut, _ := checkCmd.CombinedOutput()
		if checkCmd.ProcessState.ExitCode() == 0 {
			t.Errorf("case %d: -check accepted a program the corpus says it rejects:\n%s", i, src)
			continue
		}
		buildCmd := exec.Command(fernBin, "-target", "x86-64-linux",
			"-o", filepath.Join(progDir, "prog.bin"), prog, stdlibRoot)
		buildOut, _ := buildCmd.CombinedOutput()
		if buildCmd.ProcessState.ExitCode() == 0 {
			t.Errorf("case %d: -check rejected but -target built it (#6961).\nsrc: %s-check said: %s\n-target said: %s",
				i, src, checkOut, buildOut)
		}
	}
}

// formerlyExemptCode is one row of the #8461 matrix: a program NATIVE rejects
// with `code`, which `is_partial_checker_gap_code` used to hold off the
// self-host compile path.
type formerlyExemptCode struct {
	code string
	src  string
	// stillExempt names the issue that owns the remaining divergence, empty
	// when the code gates. A row with an issue number is a divergence someone
	// is closing; a row without one is a promise the gate keeps.
	stillExempt string
}

// TestSelfHostFormerlyExemptCodesGateX86_64 is the three-way parity matrix
// #8461 asks for: the same program through native `-check`, self-host
// `-check`, and self-host `-target`, asserting all three agree on
// accept/reject.
//
// The eighteen codes below were exempted from the compile path by
// `is_partial_checker_gap_code`, so the two front ends of ONE compiler
// disagreed about whether a program was legal and the permissive one produced
// the binary. That is invisible from any single path — both `-check` legs
// reported E041 on `xs == ys` for as long as the exemption existed, while the
// build lowered it to a pointer compare and answered "not equal" for two equal
// arrays. Reading all three at once is what makes the disagreement visible.
//
// A code drops out of `is_partial_checker_gap_code` when the checker stops
// false-positiving on it, so a row here that goes red means either a rule
// regressed or an exemption came back. Every row that is still exempt carries
// the issue that owns it: an exemption with nothing tracking it is exactly how
// this list stood at eighteen.
func TestSelfHostFormerlyExemptCodesGateX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	rows := []formerlyExemptCode{
		{code: "E001", src: "function main(): i32 { var a: i32 = zz; return a; }\n"},
		{code: "E009", src: "function main(): i32 { var s: string = \"x\"; if (s && true) { return 1; } return 0; }\n"},
		{
			code:        "E013",
			src:         "function main(): i32 { var a: i32 = 1; var a: i32 = 2; return a; }\n",
			stillExempt: "#8852",
		},
		{
			code:        "E018",
			src:         "function f(a: i32, a: i32): i32 { return a; }\nfunction main(): i32 { return f(1, 2); }\n",
			stillExempt: "#8852",
		},
		{code: "E019", src: "struct Box[T] { v: T }\nfunction f(b: Box[i32, string]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
		{code: "E021", src: "trait Greet { function hello(): i32; }\nstruct Dog {}\nimpl Greet for Dog {}\nfunction main(): i32 { return 0; }\n"},
		{code: "E024", src: "function pair(): (i32, i32) { return (1, 2); }\nfunction main(): i32 { var (a, b, c) = pair(); return a; }\n"},
		{code: "E031", src: "enum O { Aa, Bb }\nfunction main(): i32 { var o: O = O.Aa; var r = match (o) { Aa => 1, Bb => \"x\" }; return 0; }\n"},
		{code: "E034", src: "function main(): i32 { var xs: i32[] = [1, \"two\"]; return xs.len(); }\n"},
		{code: "E036", src: "enum O { Aa, Bb }\nfunction main(): i32 { var o: O = O.Cc; return 0; }\n"},
		{code: "E038", src: "function g(n: i32): i32 { return n; }\nfunction main(): i32 { return g(\"x\"); }\n"},
		{code: "E040", src: "function pick[T](a: T): T { return a; }\nfunction main(): i32 { return pick[i32, string](1); }\n"},
		{code: "E041", src: "function main(): i32 { var xs: i32[] = [1,2]; var ys: i32[] = [1,2]; if (xs == ys) { return 1; } return 0; }\n"},
		{code: "E042", src: "function main(): i32 { var n: i32 = 5; var m: i32 = n?; return m; }\n"},
		{code: "E044", src: "function nothing(): void { }\nfunction main(): i32 { var v = nothing(); var g = (): i32 => { v; return 2; }; return g(); }\n"},
		{code: "E051", src: "struct B { items: i32[] }\nfunction consume(own p: B): i32 { return p.items.len(); }\nfunction main(): i32 { var a: B = B { items: [1] }; var n: i32 = consume(a); return n + consume(a); }\n"},
		{code: "E052", src: "function f(n: i32): i32 { if (n > 0) { return 1; } }\nfunction main(): i32 { return f(1); }\n"},
		{code: "E064", src: "function f(a: Wibble): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
	}
	if len(rows) != 18 {
		t.Fatalf("the exclusion list #8461 measured held 18 codes; matrix has %d", len(rows))
	}

	for _, row := range rows {
		t.Run(row.code, func(t *testing.T) {
			progDir := t.TempDir()
			prog := filepath.Join(progDir, "prog.fern")
			if err := os.WriteFile(prog, []byte(row.src), 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}

			// Leg 1 — native is the reference (docs/NATIVE-CONVERGENCE.md):
			// the program must be one it rejects, with this code and no
			// other, so a row cannot pass on a diagnostic it did not mean.
			native := goCheckerCodes(t, progDir, row.src)
			if len(native) != 1 || native[0] != row.code {
				t.Fatalf("native reports %v, want exactly [%s] — the row no longer probes its code", native, row.code)
			}

			// Leg 2 — the self-host CHECKER. Every one of the eighteen was
			// reported here the whole time; the exemption was never about
			// the rule being absent.
			checkCmd := exec.Command(fernBin, "-check", prog, stdlibRoot)
			checkOut, _ := checkCmd.CombinedOutput()
			if checkCmd.ProcessState.ExitCode() == 0 {
				t.Fatalf("self-host -check accepted a program native rejects with %s:\n%s", row.code, checkOut)
			}
			if !strings.Contains(string(checkOut), "error["+row.code+"]") {
				t.Fatalf("self-host -check rejected but not with %s:\n%s", row.code, checkOut)
			}

			// Leg 3 — the self-host COMPILE path, which is the one that
			// disagreed.
			buildCmd := exec.Command(fernBin, "-target", "x86-64-linux",
				"-o", filepath.Join(progDir, "prog.bin"), prog, stdlibRoot)
			buildOut, _ := buildCmd.CombinedOutput()
			built := buildCmd.ProcessState.ExitCode() == 0

			if row.stillExempt != "" {
				if !built {
					t.Errorf("%s is listed as still exempt (%s) but the build now REFUSES it.\n"+
						"    Delete it from is_partial_checker_gap_code and drop stillExempt here —\n"+
						"    an exemption that no longer applies hides the next one.", row.code, row.stillExempt)
				}
				return
			}
			if built {
				t.Errorf("%s: -check rejects and -target builds it anyway — the exemption is back (#8461).\n"+
					"src: %s-check said: %s", row.code, row.src, checkOut)
			}
			if !strings.Contains(string(buildOut), "error["+row.code+"]") {
				t.Errorf("%s: the build refused but did not name the code:\n%s", row.code, buildOut)
			}
		})
	}
}
