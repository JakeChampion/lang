package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/ssa"
)

// ssa.RCSites over the real corpus, asserting the two things about it
// that are structurally true rather than the counts, which drift.
//
// This is the first analysis on docs/SSA-CUTOVER-PLAN.md's route — ask
// the ownership question where the CFG is, then map the answer back
// through Op.SrcOp — so what it needs to keep proving is that the
// mapping stays total and that the liveness answer stays sane.
//
// x86-64 only: the lift is 100% there, and the two-word configs are
// ~99% pending #7803.
func TestSSARCSitesOverTheCorpus(t *testing.T) {
	var sites, unmapped, uniqueDead int
	corpusPrograms(t, func(name string, cfg verifyConfig, ip *ir.Program) {
		if cfg.name != "x86-64" {
			return
		}
		lifted, _ := ssa.LiftProgram(ip) // lift failures are #7803's coverage
		for _, f := range lifted {
			for _, s := range ssa.RCSites(f) {
				sites++
				if !s.Mapped {
					unmapped++
				}
				// A uniqueness test exists to decide what happens to the
				// value next, so its operand is always wanted afterwards.
				// A dead one would mean the analysis had lost the use, not
				// that the compiler emitted a pointless test.
				if s.Helper == "__fern_rc_is_unique" && !s.LiveAfter {
					uniqueDead++
					if uniqueDead <= 3 {
						t.Errorf("%s: __fern_rc_is_unique at source op %d reports its operand dead "+
							"afterwards — a uniqueness test is always followed by a use of the value "+
							"it tested, so this is the liveness walk losing a use", f.Name, s.SrcOp)
					}
				}
			}
		}
	})
	if sites == 0 {
		t.Fatal("no rc sites found across the corpus — the analysis is not being exercised")
	}
	if unmapped != 0 {
		t.Errorf("%d of %d rc sites carry no source op, so their answers could not be applied "+
			"to the op stream — provenance is meant to be total (ssa.Op.SrcOp)", unmapped, sites)
	}
	t.Logf("%d rc sites across the corpus, all mapped to a source op", sites)
}
