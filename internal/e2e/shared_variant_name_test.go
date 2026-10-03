package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

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

// emitRuns is how many compiles assertEmitDeterministic compares. A resolution
// that picks the wrong enum one compile in eight still agrees 31 times in a row
// under 2% of the time.
const emitRuns = 32

// assertEmitDeterministic compiles entry to x86-64 assembly emitRuns times, each
// in its own compiler process, and requires byte-identical output. A single
// compile-and-run cannot gate an order-dependent resolution: it agrees with the
// oracle often enough to look green.
func assertEmitDeterministic(t *testing.T, entry string) {
	t.Helper()
	cli := e2eharness.SelfHostCLI(t)
	first := e2eharness.EmitAsmWithSelfHost(t, cli, e2eharness.TargetX86_64Linux, entry, nil)
	for i := 1; i < emitRuns; i++ {
		if got := e2eharness.EmitAsmWithSelfHost(t, cli, e2eharness.TargetX86_64Linux, entry, nil); got != first {
			t.Fatalf("emit %d of %d differs from the first: a variant name shared across enums or modules is resolved by an order-dependent scan", i+1, emitRuns)
		}
	}
}

func TestSharedVariantNameEmitIsDeterministic(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(sharedVariantNameSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	assertEmitDeterministic(t, src)
}
