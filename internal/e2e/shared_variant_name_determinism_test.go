package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// Two enums declaring one variant name, at DIFFERENT ordinals. `Zed` is
// index 0 in A and index 1 in B, so resolving a `B.Zed` arm against the
// wrong enum yields a real-but-wrong slot — index 0 in B is `Yy`.
//
// The lowering used to resolve match arms with a scan over every enum in
// the program (`lookupVariant`), whose loop runs over a Go map. The winner
// therefore varied per process: 24 identical compiles of this source gave
// 21 wrong answers and 3 correct ones. Arms are resolved against the
// SCRUTINEE, so an arm name ambiguous across the program is legitimate and
// reaches the IR — which is why the "the checker already rejected ambiguity"
// premise in that function's doc did not hold for arms.
const sharedVariantNameSrc = `enum A { Zed, Xx(i32) }
enum B { Yy(i32), Zed }
function b_of(v: B): i32 {
    match (v) { B.Zed => { return 4; }, B.Yy(n) => { return n * 10; } }
}
function main(): i32 { return b_of(B.Yy(1)); }
`

// emitSharedVariantAsm runs the front end + x86-64 emit once, returning the asm.
func emitSharedVariantAsm(t *testing.T, srcPath string) string {
	t.Helper()
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}

// TestSharedVariantNameEmitIsDeterministic asserts the property the bug
// violated, rather than the answer it happened to produce.
//
// Asserting the ANSWER once is not enough here: the miscompile was
// map-order dependent, so a single compile-and-run was correct about one
// time in eight and a naive regression test would have looked green. Emit
// repeatedly in one process instead and require every emission to be
// byte-identical — that fails on every run when the resolution is
// order-dependent, and it needs no toolchain.
func TestSharedVariantNameEmitIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(sharedVariantNameSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// 200, not a handful: the map layout is fixed within a process, so the
	// randomized start lands on the same enum most of the time. At 12 runs
	// this caught the bug 2 times in 5 — no better than the flakiness it is
	// meant to remove. At 200 the chance of every emission agreeing while
	// the resolution is order-dependent is ~(7/8)^200, i.e. negligible, and
	// the whole loop is still well under a second.
	first := emitSharedVariantAsm(t, srcPath)
	const runs = 200
	for i := 1; i < runs; i++ {
		if got := emitSharedVariantAsm(t, srcPath); got != first {
			t.Fatalf("emit %d of %d differs from the first: a variant name two enums share is being resolved by a map-order scan, so the emitted tag test varies per compile", i+1, runs)
		}
	}
}

// …and the answer itself, against the interpreter oracle. This one is
// subject to the same one-in-eight flakiness when the bug is present — it
// caught it on some runs and not others — so it is the companion
// assertion, not the gate. The determinism property above is the gate,
// and the conformance case `shared_variant_name` covers the shape on all
// four backends.
func TestSharedVariantNameArm64MatchesOracle(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	want := interpExit(t, interpBin, sharedVariantNameSrc)
	if want != 10 {
		t.Fatalf("interp oracle = %d, want 10 — the case itself changed", want)
	}
	if out, code := compileAndRunArm64(t, sharedVariantNameSrc); code != want {
		t.Errorf("arm64 exit = %d, want %d (interp oracle)\n%s", code, want, out)
	}
}
