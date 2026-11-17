package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A variant constructor lends to its result only through a payload that can
// hold a handle, or that is a function. `f = k(V(..))` keeps `f` fresh when no
// argument of `V` lends, and the call of the `once` function `k` needs `f`
// fresh. The qualified spelling `Owner.V(..)` gets the same answer.
func variantPayloadLendCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	cases := []struct {
		name, wake, payload, params, arg string
		reject                           bool
	}{
		{"value-payload/bare", "Wake", "V(i32[])", "seen: i32[]", "V(seen)", false},
		{"value-payload/qualified", "Wake", "V(i32[])", "seen: i32[]", "Wake.V(seen)", false},
		{"value-beside-handle-payload/bare", "Wake", "V(i32[], Reader[])", "seen: i32[]", "V(seen, [])", false},
		{"value-beside-handle-payload/qualified", "Wake", "V(i32[], Reader[])", "seen: i32[]", "Wake.V(seen, [])", false},
		{"scalar-beside-handle-payload", "Wake", "V(i32, Reader[])", "n0: i32", "Wake.V(n0, [])", false},
		{"string-beside-handle-payload", "Wake", "V(string, Reader[])", "s: string", "V(s, [])", false},
		{"handle-payload/bare", "Wake", "V(Reader)", "r: Reader", "V(r)", true},
		{"handle-payload/qualified", "Wake", "V(Reader)", "r: Reader", "Wake.V(r)", true},
		{"handles-payload/bare", "Wake", "V(i32[], Reader[])", "rs: Reader[]", "V([1], rs)", true},
		{"handles-payload/qualified", "Wake", "V(i32[], Reader[])", "seen: i32[], rs: Reader[]", "Wake.V(seen, rs)", true},
		{"function-payload/capture", "Wake", "V((i32) => Reader, Reader[])", "r: Reader", "Wake.V((i: i32): Reader => r, [])", true},
		{"function-payload/parameter", "Wake", "V((i32) => Reader, Reader[])", "g: (i32) => Reader", "V(g, [])", true},
		{"function-payload/no-handle", "Wake", "V((i32) => i32, Reader[])", "n0: i32", "Wake.V((i: i32): i32 => i + n0, [])", false},
		{"generic-payload/value", "Wake[i32[]]", "V(T, Reader[])", "seen: i32[]", "Wake.V(seen, [])", false},
		{"generic-payload/handle", "Wake[Reader]", "V(T, Reader[])", "r: Reader", "V(r, [])", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decl := "Wake"
			if strings.Contains(tc.wake, "[") {
				decl = "Wake[T]"
			}
			source := `enum ` + decl + ` { ` + tc.payload + `, TimedOut }
enum Step { Done(i32), Wait(i32, once (` + tc.wake + `) => Step) }
function count_down(n: i32): Step {
  if (n == 0) { return Done(7); }
  return Wait(n, once (w: ` + tc.wake + `): Step => count_down(n - 1));
}
function drive(` + tc.params + `): i32 {
  let f = count_down(3);
  while (true) {
    match (f) {
      Done(v) => { return v; },
      Wait(n, k) => { f = k(` + tc.arg + `); }
    }
  }
  match (f) { _ => {} }
  return -1;
}
function main(): i32 { return 0; }
`
			check := func(label string, ds []driverDiag) {
				t.Helper()
				refused := false
				for _, d := range ds {
					if d.code == "E051" {
						refused = true
					} else {
						t.Fatalf("%s: a diagnostic that is not E051: %v", label, ds)
					}
				}
				if refused != tc.reject {
					t.Fatalf("%s: E051 reported is %v, want %v: %v", label, refused, tc.reject, ds)
				}
			}
			check("Go", goCheckerDiags(t, dir, source))
			command := exec.Command(bin)
			if len(runner) > 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(source)
			check("self-host", driverDiags(runCheckerDriver(t, command, tc.name)))
		})
	}
}

func TestSelfHostVariantPayloadLendX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "variant_payload_lend_checker")
	variantPayloadLendCases(t, bin, runner, dir)
}

func TestSelfHostVariantPayloadLendWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "variant_payload_lend_checker.wasm", e2eharness.TargetWasm32Wasi)
	variantPayloadLendCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}
