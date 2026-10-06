package ir

import (
	"strings"
	"testing"
)

const verifyGateSrc = `function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(1, 2); }
`

// TestVerifyOrRefuseIsOffByDefault pins that the gate costs nothing and says
// nothing unless FERN_IR_VERIFY=1 asks for it — a malformed program passes
// through untouched, which is the pre-#8798 behaviour every compile keeps.
func TestVerifyOrRefuseIsOffByDefault(t *testing.T) {
	t.Setenv(VerifyGateEnv, "")
	p := lowerSource(t, verifyGateSrc)
	damageMain(t, p)
	if err := VerifyOrRefuse(p); err != nil {
		t.Fatalf("gate ran with the variable unset: %v", err)
	}
}

// TestVerifyOrRefuseNamesTheProblem pins the on state: a sound program passes,
// and the same program with one stack-effecting op deleted from main is
// refused with the verifier's own problem text, naming the function.
func TestVerifyOrRefuseNamesTheProblem(t *testing.T) {
	t.Setenv(VerifyGateEnv, "1")
	p := lowerSource(t, verifyGateSrc)
	if err := VerifyOrRefuse(p); err != nil {
		t.Fatalf("sound program refused: %v", err)
	}
	damageMain(t, p)
	err := VerifyOrRefuse(p)
	if err == nil {
		t.Fatal("damaged program passed the gate")
	}
	if !strings.Contains(err.Error(), VerifyGateEnv) || !strings.Contains(err.Error(), "main") {
		t.Errorf("refusal names neither the gate nor the function:\n%v", err)
	}
}

// damageMain deletes main's first OpConstI32, so the call it feeds is short
// an argument — the imbalance the stack verifier exists to report.
func damageMain(t *testing.T, p *Program) {
	t.Helper()
	for _, f := range p.Funcs {
		if f.Name != "main" {
			continue
		}
		for i, op := range f.Ops {
			if op.Kind == OpConstI32 {
				f.Ops = append(f.Ops[:i:i], f.Ops[i+1:]...)
				return
			}
		}
	}
	t.Fatal("no OpConstI32 in main to delete")
}
