package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// sourceIifeCases pin a HAND-WRITTEN immediately-invoked lambda through the
// self-host IR path (#7192).
//
// A value block and a source IIFE have the identical AST shape — a zero-arg
// call of a zero-param lambda — and the self-host told them apart by that shape
// alone. So a written IIFE got value-block treatment: irlower INLINED it, and
// its `defer` was hoisted to the enclosing function's exit. The repro answered
// 1 where native answers 6.
//
// They are not the same thing. A value block is a desugar of an if-/match-
// expression or a comprehension: not a call, no scope, and its defers correctly
// belong to the enclosing function — which is why the naive fix of "treat value
// blocks as scopes" is ruled out by TestSelfHostDeferValueBlockIR_X86_64, whose
// four subtests all refuse to lower under it. A written IIFE *is* a call with a
// scope of its own, so its defer runs when IT returns.
//
// The parser knows which one it built, so the fix marks the desugar
// (parser.ORIGIN_BLOCK / _MATCH_EXPR / _IF_EXPR / _ARR_COMP / _MAP_COMP) and
// every consumer tests the marker instead of the shape. There were five such
// consumers, not one: parser.is_value_block, irlower's is_iife_callee and
// call_bail_tag, and the two lower_iife dispatch sites.
//
// The unmarked IIFE then needs something to call, and the lift walk only ever
// looked at a `var` initialiser. Rather than teach it a second shape,
// name_source_iifes normalises the one it does not know into the one it does:
// `(function(){B})()` becomes `var $iife$f$0 = function(){B}; $iife$f$0()`, and
// try_lift_binding takes it from there. Only the binding is hoisted — building a
// lambda literal has no side effects, so evaluation order is untouched.
//
// Every case is oracle-checked against the interpreter and compiled under
// FERN_STRICT_IR, so a per-function bail is a hard failure rather than a route
// that quietly reaches the same answer. That matters here specifically: before
// the third part landed, the first two turned the wrong answer into a bail,
// which looks like progress in a divergence table and is not a fix.
var sourceIifeCases = []struct {
	name string
	src  string
}{
	// The filed repro. The defer belongs to the IIFE, so `out` is already 5 by
	// the time the enclosing return reads it: 5 + 1 = 6, not 1.
	{"var_init_iife", `function main(): i32 {
    var out = 0;
    var v = (function(): i32 { defer { out = out + 5; } return 1; })();
    return out + v;
}`},
	// Per-iteration: the defer fires each time the IIFE returns, not once at
	// main's exit. 3 * (5 + 1) = 18.
	{"iife_in_loop", `function main(): i32 {
    var out = 0;
    var i = 0;
    while (i < 3) {
        var v = (function(): i32 { defer { out = out + 5; } return 1; })();
        out = out + v;
        i = i + 1;
    }
    return out;
}`},
	// Nested IIFEs, each with its own defer. The inner one runs at the inner
	// return, the outer at the outer: 100 + 5 + 2 = 107. A single shared scope
	// would order them differently.
	{"nested_iifes", `function main(): i32 {
    var out = 0;
    var v = (function(): i32 {
        defer { out = out + 5; }
        var w = (function(): i32 { defer { out = out + 100; } return 2; })();
        return w;
    })();
    return out + v;
}`},
	// The IIFE captures an enclosing local, and the defer reads it. Naming the
	// lambda must not disturb the capture: 10 + 11 = 21.
	{"iife_captures_local", `function main(): i32 {
    var base = 10;
    var out = 0;
    var v = (function(): i32 { defer { out = out + base; } return base + 1; })();
    return out + v;
}`},
	// The defer sits inside an `if` in the IIFE body, not at its top level. The
	// stamp is driven by dl_stmts_have_defer, which recurses into if / while /
	// for / match bodies and stops only at a nested LAMBDA — whose defers are
	// its own scope. A top-level-only scan would leave this one inlined and the
	// bug intact: 5 + 1 = 6.
	{"defer_nested_in_if", `function main(): i32 {
    var out = 0;
    var c = true;
    var v = (function(): i32 { if (c) { defer { out = out + 5; } } return 1; })();
    return out + v;
}`},
	// Control: an IIFE with no defer at all. It has no scope to get wrong, and
	// it compiled before the change — this is what catches a fix that makes the
	// unmarked shape stop lowering rather than lower correctly.
	{"iife_no_defer", `function main(): i32 {
    var v = (function(): i32 { return 42; })();
    return v;
}`},
	// Control in the other direction: a genuine value block in the same program
	// as a written IIFE. The marker has to separate them per-node, not per-module
	// — the block's defer belongs to main, the IIFE's to itself.
	{"value_block_beside_iife", `function main(): i32 {
    var out = 0;
    var b = { defer { out = out + 1; } 3 };
    var v = (function(): i32 { defer { out = out + 5; } return 1; })();
    return out * 100 + b * 10 + v;
}`},
}

// TestSelfHostSourceIifeIR_X86_64 drives sourceIifeCases through the self-host
// x86-64 IR path under FERN_STRICT_IR, oracle-checked against the interpreter.
func TestSelfHostSourceIifeIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range sourceIifeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			cmd := runX86_64Bin(runner, mmc, mainPath, stdlibRoot)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			asm, cerr := cmd.Output()
			if cerr != nil {
				t.Fatalf("strict-IR compile: %v: %s", cerr, exitStderr(cerr))
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "srciife_"+tc.name, string(asm))
			run := runX86_64Bin(runner, progBin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
