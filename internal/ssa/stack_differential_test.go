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
// the lift pushed a Result for EVERY OpCall, including a void-returning
// one. `print` and `__memcpy` were the worked examples — the verifier
// pushes nothing, the lift pushed one, and the next op is an ordinary
// local.load rather than a drop, so the phantom entry just sat there.
// It never broke code generation (nothing consumed the value and DCE
// removed it), which is exactly why it survived: the model was wrong in
// a way no output ever showed. Fixed in the same change, which is what
// took one-word agreement from 99.30% to 99.77%.
//
// The arm64 column is now real. It first landed at 98.85%, which was
// an artifact: the verifier read the string ABI from a global the
// lowering had already restored, so it modelled one-word against a
// one-word lift. `ir.Func.TwoWordStr` carries the decision with the IR
// instead, and the honest figure is 77.29% — the two-word gap that
// #7803 is about, which the column had been hiding.
//
// The 359 that remain on one-word are DEFINED callees that return void.
// `providedSigs` cannot answer for those — they are the program\'s own
// functions — so closing them needs the callee table the lift does not
// have. `__map_cow_inplace` is the example. That is a bounded, named
// gap rather than an unknown one.
//
// So the gate is a RATCHET per configuration rather than a demand for
// zero. The measured agreement is recorded below; the test fails when it
// falls, which catches a regression without pretending the current state
// is correct. Whoever fixes the void-call push, or #7803's two-word slot
// model, raises the floor in the same commit.

import (
	"path/filepath"
	"slices"
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

// callKinds are the call-shaped ops, the one family whose stack effect
// the lift cannot yet reproduce. See `only`.
var callKinds = []ir.OpKind{ir.OpCallDirect, ir.OpCallIndirect, ir.OpCallDirectPair}

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
		// reachable op.
		//
		// It is the WEAKER of this test's two gates. An aggregate
		// percentage moves for reasons that have nothing to do with
		// the lift — corpus growth, a lowering change, a fix to the
		// verifier it is measured against — so it catches only a
		// collapse. `only` below is the gate that carries the signal.
		//
		// The arm64 floor was 0.985 when this test first landed, and
		// that number was measuring nothing: the verifier derived the
		// string ABI from `ast.UseTwoWordStrings`, a global the
		// lowering sets and RESTORES, so a two-word program inspected
		// afterwards was checked as one-word — against a lift that was
		// also one-word. Two wrong models agreeing is not agreement.
		//
		// Measured 2026-08-30 at 0.9977 / 0.7053 / 0.6889 and set just
		// under. The two-word columns are not comparable with the
		// figures recorded before `TypeIsTwoWordABI`: the same global
		// reached `typeSlots` and `localSlots` by a second route, so
		// the arm64 column was a hybrid of an honest call model and a
		// one-word slot model.
		floor float64
		// only names the op kinds allowed to be a function's FIRST
		// divergence. Anything else fails outright, whatever the
		// aggregate says.
		//
		// This is the real gate. Bringing an op class into agreement
		// shows up here as that class disappearing, which cannot be
		// faked by a change that merely moves the percentage — and a
		// regression in a class already modelled is caught even when
		// the corpus grew enough to hide it in the aggregate.
		//
		// Calls are what is left: the lift counts a call's operands
		// with the IR's ARGUMENT count, which undercounts as soon as
		// one of them is two-word, and it has no signature for a
		// DEFINED callee, so it cannot tell how many words one
		// returns. Both need a program-level callee table the lift is
		// not given. That is the whole of the remaining gap on all
		// three targets.
		only []ir.OpKind
	}{
		{"x86-64 one-word", 8, false, 0.995, callKinds},
		{"arm64 two-word", 8, true, 0.700, callKinds},
		{"wasm32 two-word", 4, false, 0.680, callKinds},
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
		for k, n := range firstBy {
			if !slices.Contains(cfg.only, k) {
				t.Errorf("%s: %d function(s) first diverge at %v, which this lift is "+
					"supposed to model exactly — only calls are still expected to differ",
					cfg.name, n, k)
			}
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
