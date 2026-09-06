package x86_64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

// corpusPrograms are small self-contained (import-free) programs exercising a
// spread of language features. They stand in for the real e2e corpus so the SSA
// real-asm emitter can be measured against ACTUAL checker/lowerer output rather
// than hand-built SSA — surfacing lift/emit gaps a unit test wouldn't.
var corpusPrograms = []string{
	`function add(a: i32, b: i32): i32 { return a + b; }`,
	`function abs(n: i32): i32 { if (n < 0) { return 0 - n; } else { return n; } }`,
	`function sum(n: i32): i32 {
		var total: i32 = 0;
		var i: i32 = 0;
		while (i < n) { total = total + i; i = i + 1; }
		return total;
	}`,
	`function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); }`,
	`function bits(a: i32, b: i32): i32 { return (a & b) | (a ^ b); }`,
	`function shifts(a: i32, b: i32): i32 { return (a << b) + (a >> b); }`,
	`function divmod(a: i32, b: i32): i32 { return (a / b) + (a % b); }`,
	`function fadd(a: f64, b: f64): f64 { return a + b; }`,
	`function fcmp(a: f64, b: f64): boolean { return a < b; }`,
	`function conv(n: i32): f64 { return n as f64; }`,
	`function main(): i32 { return 1 + 2 + 3; }`,

	// Composite types: struct field access, array indexing, match, string.
	`struct Point { x: i32, y: i32 } function mk(a: i32, b: i32): i32 { var p = Point { x: a, y: b }; return p.x + p.y; }`,
	`function arr(): i32 { var a = [1, 2, 3]; return a[0] + a[2]; }`,
	`function m(n: i32): i32 { return match (n) { 0 => 10, 1 => 20, _ => 30 }; }`,
	`function slen(): i32 { var x = "hi"; return x.len(); }`,

	// Closures: a returned closure over a capture, and a closure passed as an
	// argument then called indirectly.
	`function adder(n: i32): (i32) => i32 { function add(x: i32): i32 { return x + n; } return add; } function useit(): i32 { var f = adder(3); return f(4); }`,
	`function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function callit(): i32 { return apply((y: i32): i32 => { return y * 2; }, 21); }`,

	// Option construction + match (the pair-return path).
	`function half(n: i32): Option[i32] { if (n % 2 == 0) { return Some(n / 2); } return None; } function opt(): i32 { return match (half(10)) { Some(v) => v, None => 0 }; }`,
}

// TestCorpusEmitCoverage lifts each corpus program to SSA and runs every
// function through the abstract emitter, tallying how many lift and how many
// emit, plus a histogram of the ops that block emission. It asserts a floor so
// a regression that drops real-corpus coverage is caught, and logs the detail.
func TestCorpusEmitCoverage(t *testing.T) {
	var total, lifted, verified, emitted int
	liftFail := map[string]int{}
	verifyFail := map[string]int{}
	emitFail := map[string]int{}

	for pi, src := range corpusPrograms {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("program %d parse: %v", pi, err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("program %d check: %v", pi, err)
		}
		irProg, err := ir.LowerWith(prog, info, 8)
		if err != nil {
			t.Fatalf("program %d lower: %v", pi, err)
		}
		for _, fn := range irProg.Funcs {
			total++
			f, err := ssa.LiftFromIR(fn)
			if err != nil {
				liftFail[firstErrToken(err)]++
				continue
			}
			lifted++
			// Verify the lifted SSA before emitting. Emitting invalid SSA is
			// undefined, so a verify failure is a *lift* bug, tracked separately
			// from emitter coverage. (This is how the corpus surfaced the
			// match-on-Option join-phi bug — `match (opt) { Some(v) => v, None =>
			// k }` lifts to `ret` of a value not defined on the None path.)
			if err := ssa.Verify(f); err != nil {
				verifyFail[firstErrToken(err)]++
				continue
			}
			verified++
			if _, err := x86.Emit(f, 4); err != nil {
				emitFail[firstErrToken(err)]++
				continue
			}
			emitted++
		}
	}

	t.Logf("corpus coverage: %d funcs, %d lifted, %d verified, %d emitted", total, lifted, verified, emitted)
	if len(liftFail) > 0 {
		t.Logf("lift failures: %v", liftFail)
	}
	if len(verifyFail) > 0 {
		t.Logf("verify failures (lift bugs): %v", verifyFail)
	}
	if len(emitFail) > 0 {
		t.Logf("emit failures: %v", emitFail)
	}

	if verified == 0 || emitted == 0 {
		t.Fatalf("expected some functions to verify and emit, got verified=%d emitted=%d", verified, emitted)
	}
	// Every function that lifts to *valid* SSA must emit: these programs use only
	// ops the real-asm path covers, so an emit failure on a verified function is a
	// real emitter regression (e.g. the unreachable-block gap this corpus first
	// surfaced).
	if emitted != verified {
		t.Errorf("%d/%d verified functions failed to emit: %v", verified-emitted, verified, emitFail)
	}
}

// firstErrToken extracts a short bucket key from an error (the message up to the
// first colon) so failures group by cause.
func firstErrToken(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		s = s[i+2:]
	}
	return s
}
