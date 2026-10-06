package ir

import (
	"fmt"
	"os"
	"strings"
)

// VerifyGateEnv is the environment variable that runs Verify over every
// program a native backend is about to emit. `1` turns the gate on; anything
// else leaves compilation as it is. The self-host drivers read the same
// variable for their own per-function gate (irverifygate.fern).
const VerifyGateEnv = "FERN_IR_VERIFY"

// verifyGateMaxProblems bounds how many problems one refusal names.
const verifyGateMaxProblems = 20

// VerifyGateOn reports whether FERN_IR_VERIFY=1 is set.
func VerifyGateOn() bool {
	return os.Getenv(VerifyGateEnv) == "1"
}

// VerifyOrRefuse is what a backend calls on the program it is handing to its
// emitter, after every IR pass has run: under FERN_IR_VERIFY=1 it verifies
// the program and returns an error naming what the verifiers found, so the
// compile fails instead of writing a malformed module with exit 0. It also
// writes one coverage line to stderr — a verifier that reports nothing for a
// function it could not model is silent, not clean, so the line is what says
// how much was looked at. Off, it does nothing.
func VerifyOrRefuse(p *Program) error {
	if !VerifyGateOn() {
		return nil
	}
	problems, cov, _ := Verify(p)
	fmt.Fprintf(os.Stderr, "%s: %d functions verified, %d modelled by the stack verifier, %d skipped\n",
		VerifyGateEnv, cov.Funcs, cov.Modelled, len(cov.Skipped))
	if len(problems) == 0 {
		return nil
	}
	var b strings.Builder
	for i, pr := range problems {
		if i == verifyGateMaxProblems {
			fmt.Fprintf(&b, "\n  ... and %d more", len(problems)-verifyGateMaxProblems)
			break
		}
		b.WriteString("\n  ")
		b.WriteString(pr.Error())
	}
	return fmt.Errorf("%s: the lowered IR is malformed (#8798):%s", VerifyGateEnv, b.String())
}
