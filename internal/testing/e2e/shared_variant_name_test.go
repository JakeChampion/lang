package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const sharedVariantNameSrc = `enum A { Zed, Xx(i32) }
enum B { Yy(i32), Zed }
function b_of(v: B): i32 {
    match (v) { B.Zed => { return 4; }, B.Yy(n) => { return n * 10; } }
}
function main(): i32 { return b_of(B.Yy(1)); }
`

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

const emitRuns = 32

func assertEmitDeterministic(t *testing.T, entry string) {
	t.Helper()
	cli := e2eharness.SelfHostCLI(t)
	first := e2eharness.EmitAsmWithSelfHost(t, cli, e2eharness.TargetX86_64Linux, entry)
	for i := 1; i < emitRuns; i++ {
		if got := e2eharness.EmitAsmWithSelfHost(t, cli, e2eharness.TargetX86_64Linux, entry); got != first {
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
