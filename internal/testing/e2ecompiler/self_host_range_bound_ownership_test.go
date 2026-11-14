package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func testRangeBoundOwnership(t *testing.T, bin string, runner []string) {
	t.Helper()
	const prefix = `struct H { n: i32 }
function (own h: H) close(): i32 { return h.n; }
function low(own h: H): i32 { return h.close(); }
function high(h: H): i32 { return h.n; }
`
	for _, tc := range []struct{ name, bounds, want string }{
		{"consume-then-borrow", "low(h)..high(h)", "E050"},
		{"borrow-then-consume", "high(h)..low(h)", ""},
		{"inclusive-consume-then-borrow", "low(h)..=high(h)", "E050"},
		{"inclusive-borrow-then-consume", "high(h)..=low(h)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := prefix + "function f(own h: H): i32 { for i in " + tc.bounds + " {} return 0; }\nfunction main(): i32 { return 0; }\n"
			var codes []string
			for _, d := range goCheckerDiags(t, t.TempDir(), source) {
				codes = append(codes, d.code)
			}
			if got := strings.Join(uniqueSortedCodes(codes), ","); got != tc.want {
				t.Fatalf("Go codes = %q, want %q", got, tc.want)
			}
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(source)
			if got := strings.Join(driverCodes(runCheckerDriver(t, cmd, tc.name)), ","); got != tc.want {
				t.Fatalf("self-host codes = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSelfHostRangeBoundOwnershipX86_64(t *testing.T) {
	testRangeBoundOwnership(t, hostDriverBin(t, "drivers/checker_codes_run.fern"), nil)
}

func TestSelfHostRangeBoundOwnershipWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Fatal("wasmtime is required for ownership controls")
	}
	bin := e2eharness.CachedDriverBinFor(t, e2eharness.RepoPath("compiler"), "drivers/checker_codes_run.fern", e2eharness.TargetWasm32Wasi)
	testRangeBoundOwnership(t, bin, []string{wasmtime, "run"})
}
