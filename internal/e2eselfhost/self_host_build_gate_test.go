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
			src:      "function main(): i32 { var g = function(a: i32, b: i32): i32 { return a + b; }; return g(1); }\n",
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
			src:      "function dbl(a: i32, b: i32): i32 { return a + b; }\nfunction z(f: () => i32): i32 { var q = f; return q(); }\nfunction main(): i32 { var g = function(a: i32, b: i32): i32 { return a + b; }; var h = dbl; return g(1, 2) + h(3, 4) + z((): i32 => 0); }\n",
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
			// E064 is excluded: the partial checker does not know every stdlib
			// type, so "unknown type" fires on valid imports — including on the
			// compiler's own sources, which is where gating it breaks the
			// fixpoint.
			name:     "excluded-unknown-stdlib-type-E064",
			src:      "import \"std/io\";\nfunction main(): i32 { var r: i32 = 0; return r; }\n",
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
