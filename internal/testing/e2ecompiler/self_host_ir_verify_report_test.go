package e2ecompiler

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Under FERN_IR_VERIFY=1 a compile prints one coverage line on every target,
// native's (#8798): a verifier that reports nothing for a function it could
// not model is silent, not clean, so the line is what says how much was
// looked at (#11412). Without the explicit 1 the gate still runs, quietly.
func TestSelfHostIRVerifyReportsCoverage(t *testing.T) {
	cli := newStrictCLI(t)
	src := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n"
	re := regexp.MustCompile(`(?m)^FERN_IR_VERIFY: (\d+) functions verified, (\d+) modelled by the stack verifier, (\d+) skipped$`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			_, diags, err := cli.tryEmit(t, target, src, "FERN_IR_VERIFY=1")
			if err != nil {
				t.Fatalf("compile under FERN_IR_VERIFY=1 failed: %v\n%s", err, diags)
			}
			m := re.FindStringSubmatch(diags)
			if m == nil {
				t.Fatalf("no coverage line on stderr:\n%s", diags)
			}
			funcs, _ := strconv.Atoi(m[1])
			modelled, _ := strconv.Atoi(m[2])
			skipped, _ := strconv.Atoi(m[3])
			// `add` is a tiny leaf the emit inlines into main, so the count
			// is what was emitted and verified, as native's is: one.
			if funcs < 1 || funcs != modelled+skipped {
				t.Errorf("coverage %s: want at least one function, modelled + skipped = verified", m[0])
			}
			if strings.Count(diags, "FERN_IR_VERIFY: ") != 1 {
				t.Errorf("want one coverage line, got:\n%s", diags)
			}
			_, quiet, err := cli.tryEmit(t, target, src)
			if err != nil {
				t.Fatalf("compile failed: %v\n%s", err, quiet)
			}
			if strings.Contains(quiet, "FERN_IR_VERIFY") {
				t.Errorf("the gate's default run printed a line:\n%s", quiet)
			}
		})
	}
}
