package e2e

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestStringConcatCopySearchWasm(t *testing.T) {
	if code := compileAndRunWasmbinMain(t, e2eharness.StringConcatCopySearchProgram); code != 0 {
		t.Fatalf("exit = %d", code)
	}
}

func TestStringConcatCopySearchInterp(t *testing.T) {
	if code := runInterpExit(t, e2eharness.StringConcatCopySearchProgram); code != 0 {
		t.Fatalf("exit = %d", code)
	}
}

func TestArm64DarwinStringConcatCopySearch(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	if code := runArm64Darwin(t, e2eharness.StringConcatCopySearchProgram); code != 0 {
		t.Fatalf("exit = %d", code)
	}
}

func TestStringTransformInvolutionDoesNotLeak(t *testing.T) {
	gcc, runner, ok := e2eharness.LookupX86_64Tooling()
	if !ok {
		t.Skip("requires x86-64 tooling")
	}
	n, sites, err := traceOneFixture(t, gcc, runner, filepath.Join(conformanceCases, "prop_string_involution"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d unpaired allocations: %s", n, sites)
	}
}

func TestStringConcatCopySearchDoesNotLeak(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, string) (string, string, int)
	}{
		{"x86_64", runLeakCheckX86_64},
		{"arm64", runLeakCheckArm64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := tc.run(t, e2eharness.StringConcatCopySearchProgram)
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			if allocs != frees || live != 0 {
				t.Fatalf("allocs=%d frees=%d live=%d, want balanced allocations", allocs, frees, live)
			}
		})
	}
}
