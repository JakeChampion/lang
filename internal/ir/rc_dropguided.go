package ir

import (
	"sort"

	"github.com/jakechampion/lang/internal/ast"
)

// This file implements the DROP-GUIDED reuse source selection evaluated
// behind ast.RcReuseDropGuided (plan item E3, docs/NICHE-BORROWS-PLAN.md;
// the reference is Lorenzen & Leijen, "Reference Counting with
// Frame-Limited Reuse", ICFP 2022). It replaces only the SELECTION half of
// computeReuseSources — which (C, D) pairs are proposed — and shares
// everything else verbatim with the PLDI-2021 pairing: the eligibility
// gates live in the attemptPair closure this file receives, and the
// lowering (runtime is_unique guard, degrade-to-fresh-alloc, slot zeroing,
// reuseConsumed bookkeeping) is byte-for-byte the same emit path.
//
// The token model: a reuse token for donor D is BORN at D's drop point —
// the program point right after the statement holding D's last use — and
// flows FORWARD along straight-line control flow within the frame until
// the FIRST same-class construction claims it. Tokens are frame-limited:
// they never outlive or leave the function frame, and they die at any
// control-flow join/branch they cannot soundly cross. Concretely, three
// flows are implemented, each conservative:
//
//  1. Same statement list (dropGuidedSameList): the token is born at
//     lastRef+1 in D's own list and claimed by the first same-class
//     construction at or after that index — FIFO in drop order, the
//     paper's "next matching allocation" rule.
//  2. Into a dominated nested region: the shared CROSS-BLOCK pass in
//     computeReuseSources (a token born before a top-level statement
//     flows into constructions nested anywhere inside it) — this pass is
//     already token-shaped and runs unchanged under both strategies.
//  3. A drop INSIDE a dominated, non-loop arm feeding a LATER construction
//     in the same arm (dropGuidedArmPass) — the shape the PLDI pairing
//     structurally cannot see, because D is declared in the parent list
//     (so the same-block pass misses it) yet still referenced inside the
//     enclosing statement (so the cross-block deadFrom rejects it).
type reusePairingHooks struct {
	attemptPair    func(cName string, cNode ast.Expr, declIdx map[string]int, k int, deadFrom func(string, int) bool) bool
	constructionAt func(st ast.Stmt) (string, ast.Expr)
	declIndices    func(stmts []ast.Stmt) map[string]int
	deadFromIn     func(stmts []ast.Stmt) func(string, int) bool
	// reuseClassOk pre-filters donors that can never pair (non-box type,
	// excluded field kinds, oversized) so the token scan skips them cheaply;
	// attemptPair re-checks the full class match per construction.
	reuseClassOk func(name string) bool
	sources      map[ast.Expr]string
}

// dropGuidedSameList is flow 1: the per-statement-list forward token scan.
// For every block's statement list, each eligible donor D declared in the
// list gets a token born at index lastRef(D)+1 (declIdx+1 when D is never
// referenced after its declaration). Tokens are processed in drop order
// (FIFO — earliest drop first, deterministic name tie-break) and each is
// claimed by the FIRST unpaired same-class construction at or after its
// birth index. This proposes the same pair COUNT as the PLDI same-block
// pass (deadFrom(D, k) is exactly "token born at or before k"), but
// assigns donors to constructions in drop order rather than declaration
// order — the drop-guided claiming rule.
func (b *builder) dropGuidedSameList(h reusePairingHooks) {
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		blk, ok := n.(*ast.Block)
		if !ok {
			return true
		}
		stmts := blk.Stmts
		declIdx := h.declIndices(stmts)
		if len(declIdx) == 0 {
			return true
		}
		deadFrom := h.deadFromIn(stmts)
		type token struct {
			name string
			born int // first index at which the donor is dead
		}
		var tokens []token
		for name, di := range declIdx {
			if !h.reuseClassOk(name) {
				continue
			}
			born := di + 1
			for i := di + 1; i < len(stmts); i++ {
				if stmtReferencesName(stmts[i], name) {
					born = i + 1
				}
			}
			if born < len(stmts) {
				tokens = append(tokens, token{name, born})
			}
		}
		sort.Slice(tokens, func(i, j int) bool {
			if tokens[i].born != tokens[j].born {
				return tokens[i].born < tokens[j].born
			}
			return tokens[i].name < tokens[j].name
		})
		for _, tk := range tokens {
			single := map[string]int{tk.name: declIdx[tk.name]}
			for k := tk.born; k < len(stmts); k++ {
				cName, cNode := h.constructionAt(stmts[k])
				if cNode == nil {
					continue
				}
				if _, done := h.sources[cNode]; done {
					continue
				}
				if h.attemptPair(cName, cNode, single, k, deadFrom) {
					break // token claimed by the first matching construction
				}
			}
		}
		return true
	})
}

// dropGuidedArmPass is flow 3: a token born INSIDE a dominated, non-loop
// arm. Donor D is declared in a parent statement list P; its last use sits
// inside one arm A of a non-loop control statement S = P[j]; a construction
// C later in A (index k) claims the token. Sound because on the path
// through A the drop point dominates C, and on every other path (sibling
// arm, S not taken) the reuse never runs and D's intact slot reaches the
// exit sweep / reinit drop as usual — the same zeroed-slot protocol the
// cross-block pass relies on. Conservative gates:
//
//   - S must be an if / match / bare block — never a loop. A loop's next
//     iteration re-executes the arm prefix, re-reading a D whose box the
//     previous iteration already claimed. (D declared IN a loop body is
//     fine and handled by flows 1-2: it is re-declared each iteration.)
//   - No reference to D after S in P (deadFromIn(P)(D, j+1)).
//   - Every reference to D inside S must sit in the claimed arm's prefix
//     A[0..k-1], except if-chain CONDITIONS (they evaluate before any arm
//     runs and bind nothing). Match scrutinees and guards are NOT allowed:
//     a `match (d)` introduces payload bindings that alias D's box inside
//     the arm, exactly the view a reuse there could free from under.
//     Sibling-arm references are rejected wholesale (a sibling ref is
//     actually unreachable from C, but proving that adds nothing today).
//
// Everything else — class match, freeEligible, never-reassigned,
// name-unique, moved/borrow exclusions, one claim per donor — is
// attemptPair's shared gate set, identical to the PLDI pairing.
func (b *builder) dropGuidedArmPass(h reusePairingHooks) {
	ast.Walk(b.fn.Body, func(n ast.Node) bool {
		blk, ok := n.(*ast.Block)
		if !ok {
			return true
		}
		stmts := blk.Stmts
		declIdx := h.declIndices(stmts)
		if len(declIdx) == 0 {
			return true
		}
		deadAfter := h.deadFromIn(stmts)
		for j, st := range stmts {
			arms, preArm := dominatedArms(st)
			if len(arms) == 0 {
				continue
			}
			for _, arm := range arms {
				for k, ist := range arm.Stmts {
					cName, cNode := h.constructionAt(ist)
					if cNode == nil {
						continue
					}
					if _, done := h.sources[cNode]; done {
						continue
					}
					armDead := func(name string, _ int) bool {
						return deadAfter(name, j+1) && armConfinesRefs(st, preArm, arm, k, name)
					}
					h.attemptPair(cName, cNode, declIdx, j, armDead)
				}
			}
		}
		return true
	})
}

// dominatedArms returns the immediate arm blocks of a NON-LOOP control
// statement — the regions a drop-guided token may be born in — plus the
// pre-arm expressions (if-chain conditions) whose references to a donor
// are harmless because they evaluate to completion before any arm runs
// and introduce no bindings. Loops (while / for / loop / for-each) return
// nil: a token must never cross a loop back-edge (see dropGuidedArmPass).
// Match Tag / guards are deliberately NOT in preArm — their references
// disqualify via armConfinesRefs, because match binds payload views of
// the scrutinee that stay live inside the arm.
func dominatedArms(st ast.Stmt) (arms []*ast.Block, preArm []ast.Expr) {
	switch s := st.(type) {
	case *ast.If:
		cur := ast.Stmt(s)
		for {
			ifn, ok := cur.(*ast.If)
			if !ok {
				// A trailing non-if else (a *ast.Block) is an arm.
				if eb, ok := cur.(*ast.Block); ok {
					arms = append(arms, eb)
				}
				break
			}
			preArm = append(preArm, ifn.Cond)
			if tb, ok := ifn.Then.(*ast.Block); ok {
				arms = append(arms, tb)
			}
			if ifn.Else == nil {
				break
			}
			cur = ifn.Else
		}
	case *ast.Match:
		for _, a := range s.Arms {
			if a.Body != nil {
				arms = append(arms, a.Body)
			}
		}
	case *ast.Block:
		arms = append(arms, s)
	}
	return arms, preArm
}

// armConfinesRefs reports whether EVERY reference to `name` inside the
// whole statement S sits in an allowed region: the pre-arm expressions or
// the claimed arm's prefix A[0..k-1]. Counting totals makes the check
// fail-safe for any statement shape dominatedArms does not model — an
// unmodeled region's references inflate the total without appearing in
// the allowed count, rejecting the pair.
func armConfinesRefs(st ast.Stmt, preArm []ast.Expr, arm *ast.Block, k int, name string) bool {
	allowed := 0
	for _, e := range preArm {
		allowed += countNameRefs(e, name)
	}
	for i := 0; i < k && i < len(arm.Stmts); i++ {
		allowed += countNameRefs(arm.Stmts[i], name)
	}
	return countNameRefs(st, name) == allowed
}

// countNameRefs counts *ast.Ident occurrences of `name` under n.
func countNameRefs(n ast.Node, name string) int {
	c := 0
	ast.Walk(n, func(nd ast.Node) bool {
		if id, ok := nd.(*ast.Ident); ok && id.Name == name {
			c++
		}
		return true
	})
	return c
}
