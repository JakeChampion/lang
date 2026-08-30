package ssa_test

// The two models of the operand stack, compared per op.
//
// `internal/ir`'s verifier and `internal/ssa`'s lift each maintain
// their own idea of how many entries every op leaves. Under the
// one-word string ABI they agree everywhere. Under two-word they do
// not, and #7803 is the consequence: a string is one entry to the lift
// and two to the verifier, so the lift's stack runs short and errors at
// the first op that notices — which is almost never the op that
// diverged.
//
// Two attempts to close that by watching an aggregate coverage number
// failed, because the number cannot separate "this fix exposed an older
// divergence" from "this fix caused one". A per-op comparison can: it
// names the FIRST index where the two disagree, which is the op whose
// slot model is wrong.
//
// What it found immediately, on the ABI that was supposed to be clean:
// the lift pushes a Result for EVERY OpCall, including a void-returning
// one. `print` and `__memcpy` are the worked examples — the verifier
// pushes nothing, the lift pushes one, and the next op is an ordinary
// local.load rather than a drop, so the phantom entry just sits there.
// It does not break code generation (nothing consumes the value and DCE
// removes it), which is exactly why it survived: the model is wrong in a
// way no output ever showed.
//
// So the gate is a RATCHET per configuration rather than a demand for
// zero. The measured agreement is recorded below; the test fails when it
// falls, which catches a regression without pretending the current state
// is correct. Whoever fixes the void-call push, or #7803's two-word slot
// model, raises the floor in the same commit.

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/ssa"
)

func lowerFixture(t *testing.T, path string, ptrW int, twoWord bool) (*ir.Program, bool) {
	t.Helper()
	prog, _, err := modload.Load(path)
	if err != nil {
		return nil, false
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return nil, false
	}
	info, err := checker.Check(prog)
	if err != nil {
		return nil, false
	}
	if err := monomorph.Run(prog, info); err != nil {
		return nil, false
	}
	pf, pt := ast.RcFreeEnabled, ast.TwoWordOverride
	ast.RcFreeEnabled, ast.TwoWordOverride = true, twoWord
	defer func() { ast.RcFreeEnabled, ast.TwoWordOverride = pf, pt }()
	ip, err := ir.LowerWith(prog, info, ptrW)
	if err != nil {
		return nil, false
	}
	return ip, true
}

// divergence is the first op where the two models disagree about a
// REACHABLE point, or -1.
//
// Unreachable points are skipped rather than compared: after a
// terminator the verifier goes polymorphic and the lift drops the
// block, so their heights there are two different spellings of
// "nothing" and comparing them is meaningless.
func divergence(want []ir.StackAt, fn *ir.Func) (at, wantH, gotH int, liftErr error) {
	got, err := ssa.StackHeights(fn)
	for i := range got {
		if i >= len(want) {
			break
		}
		if !want[i].Reachable || !got[i].Reachable {
			continue
		}
		if got[i].Height != want[i].Height {
			return i, want[i].Height, got[i].Height, err
		}
	}
	return -1, 0, 0, err
}

func TestLiftAgreesWithTheVerifiersStackModel(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the conformance corpus; not a -short test")
	}
	cases, err := filepath.Glob(filepath.Join("..", "..", "conformance", "cases", "*", "main.fern"))
	if err != nil || len(cases) < 100 {
		t.Fatalf("found %d fixtures; the corpus glob is wrong (%v)", len(cases), err)
	}

	for _, cfg := range []struct {
		name    string
		ptrW    int
		twoWord bool
		// floor is the fraction of functions that must agree at every
		// reachable op. Measured 2026-08-30 at 0.9930 / 0.9840 /
		// 0.6231 and set just under, so ordinary corpus growth does
		// not trip it but a real regression does.
		floor float64
	}{
		{"x86-64 one-word", 8, false, 0.990},
		{"arm64 two-word", 8, true, 0.980},
		{"wasm32 two-word", 4, false, 0.615},
	} {
		var compared, agreed int
		firstBy := map[ir.OpKind]int{}
		var sample string
		for _, path := range cases {
			ip, ok := lowerFixture(t, path, cfg.ptrW, cfg.twoWord)
			if !ok {
				continue
			}
			heights, _ := ir.StackHeights(ip)
			for _, fn := range ip.Funcs {
				want, modelled := heights[fn.Name]
				if !modelled {
					// The verifier abandoned it, so there is no
					// second model to compare against.
					continue
				}
				compared++
				at, wantH, gotH, _ := divergence(want, fn)
				if at < 0 {
					agreed++
					continue
				}
				firstBy[fn.Ops[at].Kind]++
				if sample == "" {
					sample = fn.Name + " op[" + itoa(at) + "] " + fn.Ops[at].Kind.String() +
						": verifier " + itoa(wantH) + ", lift " + itoa(gotH)
				}
			}
		}
		if compared == 0 {
			t.Fatalf("%s: nothing was compared", cfg.name)
		}
		t.Logf("%-18s %d/%d functions agree at every op", cfg.name, agreed, compared)
		for k, n := range firstBy {
			t.Logf("     first divergence at %-16v %d", k, n)
		}
		if sample != "" {
			t.Logf("     e.g. %s", sample)
		}
		rate := float64(agreed) / float64(compared)
		if rate < cfg.floor {
			t.Errorf("%s: only %.2f%% of functions agree with the verifier's stack model, "+
				"under the %.2f%% floor — the lift and the verifier have drifted further apart",
				cfg.name, 100*rate, 100*cfg.floor)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
