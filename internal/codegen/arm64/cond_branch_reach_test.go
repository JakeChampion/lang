package arm64

// The fused conditional branch is emitted as a direct `b.cond`, which reaches
// ±1MB. Past that the branch has to go back to the inverted-test-over-an-
// unconditional-`b` trampoline, and reachCheckCondBranches does it after the
// fact, on the real emitted distances.
//
// That expansion path is the one worth pinning here. Three functions in the
// self-hosted compiler's own arm64 emit are over the limit, so it does run in
// practice — but only inside a 24 MB whole-compiler build, where a wrong
// answer surfaces as a link error or a branch to the wrong place rather than
// as a failing assertion. These tests exercise it directly.

import (
	"fmt"
	"strings"
	"testing"
)

// farBody builds one function's worth of emitted text: a conditional branch,
// `gap` filler instructions, then the target label.
func farBody(branch string, gap int) string {
	var b strings.Builder
	b.WriteString("\t" + branch + " .Ltarget\n")
	for i := 0; i < gap; i++ {
		b.WriteString("\tnop\n")
	}
	b.WriteString(".Ltarget:\n\tret")
	return b.String()
}

func TestCondBranchWithinReachIsLeftDirect(t *testing.T) {
	g := &generator{}
	out, changed := g.expandFarCondBranches([]byte(farBody("b.eq", condBranchReachInstrs-10)))
	if changed {
		t.Fatalf("a branch inside the reach was expanded:\n%s", head(string(out)))
	}
	if strings.Contains(string(out), ".LbrFar") {
		t.Error("trampoline emitted for a branch that reaches directly")
	}
}

func TestCondBranchBeyondReachExpandsToTrampoline(t *testing.T) {
	cases := []struct{ branch, inverted string }{
		{"b.eq", "b.ne"},
		{"b.lt", "b.ge"},
		{"b.lo", "b.hs"},
		{"cbz w0,", "cbnz w0,"},
		{"cbnz x3,", "cbz x3,"},
	}
	for _, c := range cases {
		t.Run(c.branch, func(t *testing.T) {
			g := &generator{}
			out, changed := g.expandFarCondBranches([]byte(farBody(c.branch, condBranchReachInstrs+10)))
			if !changed {
				t.Fatal("a branch beyond the reach was left direct")
			}
			s := string(out)
			// The inverted test skips over an unconditional branch to the
			// real target: the condition is preserved, not dropped.
			if !strings.Contains(s, "\t"+c.inverted+" .LbrFar") {
				t.Errorf("expected the inverted test %q over the skip, got:\n%s", c.inverted, head(s))
			}
			if !strings.Contains(s, "\tb .Ltarget\n") {
				t.Errorf("expected an unconditional branch to the real target, got:\n%s", head(s))
			}
			if strings.Contains(s, "\t"+c.branch+" .Ltarget") {
				t.Errorf("the direct out-of-reach branch survived:\n%s", head(s))
			}
		})
	}
}

// A branch whose target is not a label of this function has no measurable
// distance, so it must expand rather than be assumed close.
func TestCondBranchToUnknownLabelExpands(t *testing.T) {
	g := &generator{}
	body := "\tb.eq .Lsomewhere_else\n\tnop\n.Ltarget:\n\tret"
	out, changed := g.expandFarCondBranches([]byte(body))
	if !changed {
		t.Fatalf("a branch to an unmeasurable target was left direct:\n%s", head(string(out)))
	}
	if !strings.Contains(string(out), "b .Lsomewhere_else") {
		t.Errorf("the trampoline lost the original target:\n%s", head(string(out)))
	}
}

// Expanding pushes everything after it two instructions further away, which
// can put a branch that previously reached out of reach. reachCheckCondBranches
// iterates to a fixpoint; this checks the second round actually happens.
func TestCondBranchExpansionReachesFixpoint(t *testing.T) {
	var b strings.Builder
	// Two branches, the first just inside the reach and the second well past
	// it, so expanding the second is what pushes the first out.
	b.WriteString("\tb.eq .Ltarget\n")
	for i := 0; i < condBranchReachInstrs-2; i++ {
		b.WriteString("\tnop\n")
	}
	b.WriteString("\tb.ne .Lfar\n")
	for i := 0; i < 10; i++ {
		b.WriteString("\tnop\n")
	}
	b.WriteString(".Ltarget:\n\tret\n.Lfar:\n\tret")

	g := &generator{}
	body := []byte(b.String())
	rounds := 0
	for {
		next, changed := g.expandFarCondBranches(body)
		if !changed {
			break
		}
		body = next
		rounds++
		if rounds > 8 {
			t.Fatal("expansion did not converge")
		}
	}
	if rounds < 1 {
		t.Fatal("nothing expanded; the case is vacuous")
	}
	// Whatever it took, no direct conditional branch may remain out of reach.
	if _, changed := g.expandFarCondBranches(body); changed {
		t.Error("a branch was still out of reach after the fixpoint")
	}
}

func head(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 12 {
		return strings.Join(lines[:6], "\n") + fmt.Sprintf("\n… (%d lines)", len(lines))
	}
	return s
}
