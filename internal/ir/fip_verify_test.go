package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

// E2' verify-and-enable (fip_verify.go): after lowering, a `fip` / `fbip`
// function's emitted ops are checked against the annotation's allocation
// budget — an un-reuse-paired constructor beyond the graded allowance is an
// E068 error from LowerWith. These pin both directions per shape; the
// checker-side (E053) tests live in internal/checker/fip_test.go.

// lowerErrForTest runs the full parse → check → LowerWith pipeline and
// returns LowerWith's error (nil when the program lowers clean). Free +
// reuse stay at their production defaults (on) — the configuration the
// verification is defined against.
func lowerErrForTest(t *testing.T, src string) error {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	_, err = ir.LowerWith(prog, info, 8)
	return err
}

func wantE068(t *testing.T, name, src string) {
	t.Helper()
	err := lowerErrForTest(t, src)
	if err == nil {
		t.Fatalf("%s: expected E068 (fip/fbip allocation verification), got none", name)
	}
	if !strings.Contains(err.Error(), "E068") {
		t.Errorf("%s: expected an E068 error, got: %v", name, err)
	}
}

func wantLowerOK(t *testing.T, name, src string) {
	t.Helper()
	if err := lowerErrForTest(t, src); err != nil {
		t.Errorf("%s: expected clean lowering, got: %v", name, err)
	}
}

// The R4 consuming-match rebuild — `match` on an `own` enum param whose arm
// returns a same-enum constructor — is reuse-paired (consumingMatchReuse), so
// it verifies as bare `fbip` (zero fresh sites).
func TestFbipVerifyConsumingMatchReuse(t *testing.T) {
	wantLowerOK(t, "R4 consuming match", `enum List { Cons(i32, List), Nil }
fbip function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`)
}

// The R1 struct self-overwrite (`p = P { … }` on a uniquely-owned p) is
// handled by tryStructReuseOverwrite — no fresh alloc, verifies as bare fbip.
func TestFbipVerifySelfOverwriteReuse(t *testing.T) {
	wantLowerOK(t, "R1 self-overwrite", `struct P { x: i32, y: i32 }
fbip function bump(own p: P): P {
    p = P { x: p.x + 1, y: p.y };
    return p;
}
function main(): i32 { var q: P = bump(P { x: 1, y: 2 }); return q.x; }`)
}

// The R3 general pairing: a construction takes over a DIFFERENT dead owned
// local's box. The first construction (no donor) is fresh — bare fbip fails,
// fbip(1) covers it and credits the paired second site.
func TestFbipVerifyGeneralPairingCredited(t *testing.T) {
	const body = ` function churn(a0: i32): i32 {
    var a: P = P { x: a0, y: a0 + 1 };
    var s: i32 = a.x + a.y;
    var b: P = P { x: s + 1, y: a0 };
    return b.x + b.y;
}
function main(): i32 { return churn(3); }`
	wantE068(t, "bare fbip: initial box is fresh", "struct P { x: i32, y: i32 }\nfbip"+body)
	wantLowerOK(t, "fbip(1): fresh initial box within allowance", "struct P { x: i32, y: i32 }\nfbip(1)"+body)
}

// An un-paired construction (nothing dead to reuse) fails bare fbip with an
// E068 naming the function and the site.
func TestFbipVerifyUnpairedConstructionRejected(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
fbip function mk(a: i32): P { return P { x: a, y: a + 1 }; }
function main(): i32 { var p: P = mk(3); return p.x; }`
	err := lowerErrForTest(t, src)
	if err == nil {
		t.Fatal("expected E068 for an un-paired fbip construction, got none")
	}
	msg := err.Error()
	for _, want := range []string{"E068", `"mk"`, `struct literal "P"`, "allowance of 0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("E068 message should contain %q, got: %v", want, msg)
		}
	}
}

// Graded `fip(n)`: exactly n fresh constructor allocations are permitted —
// fip(1) admits one and rejects two.
func TestGradedFipAllowanceCounted(t *testing.T) {
	wantLowerOK(t, "fip(1) with one fresh ctor", `struct P { x: i32, y: i32 }
fip(1) function mk(a: i32): P { return P { x: a, y: a + 1 }; }
function main(): i32 { var p: P = mk(3); return p.x; }`)

	src := `struct P { x: i32, y: i32 }
fip(1) function mk2(a: i32): i32 {
    var p: P = P { x: a, y: a };
    var q: P = P { x: a + 1, y: p.x };
    return p.y + q.x;
}
function main(): i32 { return mk2(3); }`
	err := lowerErrForTest(t, src)
	if err == nil {
		t.Fatal("expected E068 for two fresh ctors under fip(1), got none")
	}
	if !strings.Contains(err.Error(), "2 un-reused allocation site(s)") ||
		!strings.Contains(err.Error(), "allowance of 1") {
		t.Errorf("E068 should report the count vs the allowance, got: %v", err)
	}
}

// Bare `fip` bodies pass E053 with no allocating construct, so lowering must
// find zero allocation ops — the drift assertion. A representative
// allocation-free fip function lowers clean.
func TestFipVerifyZeroAllocClean(t *testing.T) {
	wantLowerOK(t, "bare fip in-place sort", `fip function sort_inplace(own arr: i32[]): i32[] {
    var n: i32 = arr.len();
    var k: i32 = 1;
    while (k < n) {
        var key: i32 = arr[k];
        var j: i32 = k - 1;
        while (j >= 0 && arr[j] > key) { arr = arr.with(j + 1, arr[j]); j = j - 1; }
        arr = arr.with(j + 1, key);
        k = k + 1;
    }
    return arr;
}
function main(): i32 { return 0; }`)
}

// The verification is read-only over the DEFAULT lowering configuration:
// with the reuse layer force-disabled (debug/differential runs) the pairing
// machinery is deliberately off, so the claim is skipped rather than
// mis-reported — the R4 program must still lower clean under the flag.
func TestFbipVerifySkippedWhenReuseDisabled(t *testing.T) {
	prev := ast.RcReuseEnabled
	ast.RcReuseEnabled = false
	defer func() { ast.RcReuseEnabled = prev }()
	wantLowerOK(t, "reuse off: verification skipped", `enum List { Cons(i32, List), Nil }
fbip function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`)
}

// The runtime is_unique fallback does NOT count: a paired site keeps its
// __alloc_reuse call (whose shared-input branch allocates internally), and
// the verification still reads it as zero fresh sites — pinned here by the
// same R4 function lowering with exactly one __alloc_reuse and no OpAlloc.
func TestFbipVerifyPairedSiteKeepsRuntimeGuard(t *testing.T) {
	ip := lowerForTest(t, `enum List { Cons(i32, List), Nil }
fbip function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`)
	f := funcByName(ip, "map_inc")
	if f == nil {
		t.Fatal("no func map_inc")
	}
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("paired site should lower to one __alloc_reuse, got %d", got)
	}
	for _, op := range f.Ops {
		if op.Kind == ir.OpAlloc {
			t.Errorf("verified fbip function should emit no OpAlloc, found one at %s", op.Pos)
		}
	}
}
