package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

// drive sets f from a call of a once function whose argument is a variant
// that holds the borrowed parameter seen. When the payload can hold a handle,
// f can borrow from seen, so the call of k, which f owns, is E051.
const variantLenderSource = `enum Wake { Woke(PAYLOAD), TimedOut }

enum Step { Done(i32), Wait(i32, once (Wake) => Step) }

function count_down(n: i32): Step {
  if (n == 0) {
    return Done(7);
  }
  return Wait(n, once (w: Wake): Step => count_down(n - 1));
}

function drive(seen: PAYLOAD): i32 {
  let f = count_down(3);
  while (true) {
    match (f) {
      Done(v) => {
        return v;
      },
      Wait(n, k) => {
        f = k(CTOR(seen));
      }
    }
  }
  match (f) {
    _ => {}
  }
  return -1;
}

function main(): i32 { return 0; }
`

func TestSelfHostVariantConstructorLendsByPayloadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "variant_lender_checker")
	cases := []struct{ name, payload, ctor, want string }{
		{"scalar-array-plain", "i32[]", "Woke", ""},
		{"scalar-array-qualified", "i32[]", "Wake.Woke", ""},
		{"string-array-qualified", "string[]", "Wake.Woke", ""},
		{"handle-plain", "Dir", "Woke", "E051"},
		{"handle-qualified", "Dir", "Wake.Woke", "E051"},
		{"handle-array-qualified", "Dir[]", "Wake.Woke", "E051"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := strings.ReplaceAll(strings.ReplaceAll(variantLenderSource, "PAYLOAD", tc.payload), "CTOR", tc.ctor)
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if tc.want == "" && len(ds) == 0 {
					return
				}
				for _, d := range ds {
					if tc.want != "" && d.code == tc.want {
						return
					}
				}
				t.Fatalf("%s: want %q, got %v", label, tc.want, ds)
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
