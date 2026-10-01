package e2eselfhost

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostStringByteTransforms(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, e2eharness.StringByteTransformsProgram, target); code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostStringTransformInvolutionDoesNotLeak(t *testing.T) {
	source, err := os.ReadFile("../../conformance/cases/prop_string_involution/main.fern")
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, string(source), target, "FERN_SEM_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostStringConcatCopySearchDoesNotLeak(t *testing.T) {
	testSelfHostStringConcatCopySearch(t, "1")
}

func TestSelfHostStringConcatCopySearchLegacyAST(t *testing.T) {
	// Empty selects legacy AST lowering; "0" also enables semantic IR.
	// Main already leaks in this scenario through legacy ownership (#4451).
	// Exercise behavior and sanitization here, with strict census above.
	testSelfHostStringConcatCopySearch(t, "")
}

func testSelfHostStringConcatCopySearch(t *testing.T, mode string) {
	t.Helper()
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run("typed="+mode+"/"+target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, e2eharness.StringConcatCopySearchProgram, target,
				"FERN_SEM_IR="+mode, "FERN_SEM_IR_ONLY=", "FERN_SEM_IR_SKIP=",
				"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK="+mode)
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			if mode != "" {
				assertBalancedCensus(t, stderr)
			}
		})
	}
}
