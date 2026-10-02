package e2e

import "testing"

// Two enums declaring one variant name, at DIFFERENT ordinals. `Zed` is
// index 0 in A and index 1 in B, so resolving a `B.Zed` arm against the
// wrong enum yields a real-but-wrong slot — index 0 in B is `Yy`.
//
// Arms resolve against the SCRUTINEE, so an arm name ambiguous across the
// program is legitimate and reaches the IR; resolving it by a program-wide
// scan picks whichever enum the scan meets first.
const sharedVariantNameSrc = `enum A { Zed, Xx(i32) }
enum B { Yy(i32), Zed }
function b_of(v: B): i32 {
    match (v) { B.Zed => { return 4; }, B.Yy(n) => { return n * 10; } }
}
function main(): i32 { return b_of(B.Yy(1)); }
`

// The answer against the interpreter oracle, on both compiled targets. The
// conformance case `shared_variant_name` covers the shape too.
func TestSharedVariantNameMatchesOracle(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	want := interpExit(t, interpBin, sharedVariantNameSrc)
	if want != 10 {
		t.Fatalf("interp oracle = %d, want 10 — the case itself changed", want)
	}
	t.Run("x86_64", func(t *testing.T) {
		if out, code := compileAndRunX86_64(t, sharedVariantNameSrc); code != want {
			t.Errorf("x86-64 exit = %d, want %d (interp oracle)\n%s", code, want, out)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if out, code := compileAndRunArm64(t, sharedVariantNameSrc); code != want {
			t.Errorf("arm64 exit = %d, want %d (interp oracle)\n%s", code, want, out)
		}
	})
}
