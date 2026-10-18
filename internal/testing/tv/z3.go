package tv

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

// Z3 returns z3's path, skipping the test when it is not on PATH.
// FERN_REQUIRE_Z3=1 turns the skip into a failure, for a lane that installs it.
func Z3(t testing.TB) string {
	t.Helper()
	p, err := exec.LookPath("z3")
	if err == nil {
		return p
	}
	if envflag.On("FERN_REQUIRE_Z3") {
		t.Fatal("z3 not on PATH and FERN_REQUIRE_Z3=1: this lane installs it, so a skip here covers nothing")
	}
	t.Skip("z3 not on PATH; skipping translation validation")
	return ""
}

// timeoutMs bounds each query; a query the solver gives up on is reported as
// unknown, never as proved.
const timeoutMs = 30000

// Prove runs every query through one z3 process and returns each one's
// answer: "unsat" (proved), "sat" (a counterexample exists) or "unknown".
func Prove(z3 string, queries []string) ([]string, error) {
	if len(queries) == 0 {
		return nil, nil
	}
	var in strings.Builder
	in.WriteString("(set-logic QF_BV)\n")
	for _, q := range queries {
		in.WriteString("(push)\n" + q + "(pop)\n")
	}
	out, err := run(z3, in.String())
	if err != nil {
		return nil, err
	}
	answers := strings.Fields(out)
	if len(answers) != len(queries) {
		return nil, fmt.Errorf("z3 answered %d of %d queries:\n%s", len(answers), len(queries), out)
	}
	for _, a := range answers {
		if a != "sat" && a != "unsat" && a != "unknown" {
			return nil, fmt.Errorf("z3 answered %q:\n%s", a, out)
		}
	}
	return answers, nil
}

// Counterexample re-runs a sat query and returns the solver's model: the
// entry stack and locals on which the two sides disagree.
func Counterexample(z3, query string) (string, error) {
	return run(z3, "(set-logic QF_BV)\n"+query+"(get-model)\n")
}

func run(z3, input string) (string, error) {
	cmd := exec.Command(z3, "-smt2", "-in", fmt.Sprintf("-t:%d", timeoutMs))
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("z3: %v\n%s", err, out)
	}
	return string(out), nil
}
