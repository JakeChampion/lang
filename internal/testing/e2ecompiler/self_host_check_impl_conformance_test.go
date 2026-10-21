package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestSelfHostCheckImplConformanceDiagnostics holds the E021s an impl's
// conformance check reports — a wrong signature, a missing method, a missing
// supertrait impl, an unbound associated type — to the Go checker's, position
// and text: each is reported at the `impl`, and a wrong signature spells both
// function types.
func TestSelfHostCheckImplConformanceDiagnostics(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	cases := []struct{ name, src string }{
		{"associated-function-signature", "trait Mk { function mk(x: Option[i32]): Self; }\nstruct S { v: i32 }\nimpl Mk for S { function mk(x: i32): Self { return S { v: x }; } }\n"},
		{"receiver-at-a-concrete-type", "trait Tr { function d(self: i32): i32; }\nstruct S { v: i32 }\nimpl Tr for S { function d(self: Self): i32 { return self.v; } }\n"},
		{"method-signature", "trait Tr { function d(self: Self, a: i32): void; }\nstruct S { v: i32 }\nimpl Tr for S { function d(self: Self, a: string): void { } }\n"},
		{"missing-method", "trait Tr { function d(self: Self): i32; }\nstruct S { v: i32 }\nimpl Tr for S { }\n"},
		{"missing-supertrait-impl", "trait A { function a(self: Self): i32; }\ntrait B: A { function b(self: Self): i32; }\nstruct S { v: i32 }\nimpl B for S { function b(self: Self): i32 { return 1; } }\n"},
		{"unbound-associated-type", "trait It { type Item; function n(self: Self): i32; }\nstruct S { v: i32 }\nimpl It for S { function n(self: Self): i32 { return 1; } }\n"},
	}
	diag := regexp.MustCompile(`(?m)^[^\s:]+:(\d+:\d+): error\[(\w+)\]: (.*)$`)
	diags := func(out []byte) []string {
		var ds []string
		for _, m := range diag.FindAllStringSubmatch(string(out), -1) {
			ds = append(ds, m[2]+"@"+m[1]+" "+m[3])
		}
		return ds
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(p, []byte(tc.src+"function main(): i32 { return 0; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			goOut, _ := exec.Command(oracle, "-check", p).CombinedOutput()
			shOut, _ := exec.Command(cli, "-check", p, root).CombinedOutput()
			want, got := diags(goOut), diags(shOut)
			if len(want) == 0 {
				t.Fatalf("the Go checker drew no positioned diagnostic\n%s", goOut)
			}
			if !slices.Equal(got, want) {
				t.Errorf("self-host:\n  %q\nGo checker:\n  %q", got, want)
			}
		})
	}
}
