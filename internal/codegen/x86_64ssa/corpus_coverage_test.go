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
}

// TestCorpusEmitCoverage lifts each corpus program to SSA and runs every
// function through the abstract emitter, tallying how many lift and how many
// emit, plus a histogram of the ops that block emission. It asserts a floor so
// a regression that drops real-corpus coverage is caught, and logs the detail.
func TestCorpusEmitCoverage(t *testing.T) {
	var total, lifted, emitted int
	liftFail := map[string]int{}
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
			if _, err := x86.Emit(f, 4); err != nil {
				emitFail[firstErrToken(err)]++
				continue
			}
			emitted++
		}
	}

	t.Logf("corpus coverage: %d funcs, %d lifted (%d lift-failed), %d emitted (%d emit-failed)",
		total, lifted, total-lifted, emitted, lifted-emitted)
	if len(liftFail) > 0 {
		t.Logf("lift failures: %v", liftFail)
	}
	if len(emitFail) > 0 {
		t.Logf("emit failures: %v", emitFail)
	}

	if lifted == 0 || emitted == 0 {
		t.Fatalf("expected some functions to lift and emit, got lifted=%d emitted=%d", lifted, emitted)
	}
	// Every function that lifts must also emit: these programs use only ops the
	// real-asm path covers, so an emit failure is a real regression (e.g. the
	// unreachable-block gap this corpus first surfaced).
	if emitted != lifted {
		t.Errorf("%d/%d lifted functions failed to emit: %v", lifted-emitted, lifted, emitFail)
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
