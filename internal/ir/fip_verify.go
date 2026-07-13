package ir

// IR-level verify-and-enable for the `fip` / `fbip` function modifiers
// (plan E2', docs/NICHE-BORROWS-PLAN.md; the "visibility" half of
// docs/REUSE-CONTRACT.md). The checker's E053 walk enforces the SHAPE
// rules; this pass closes the loop by checking the ops the lowering
// ACTUALLY emitted against the annotation's allocation budget:
//
//   - `fip`      — zero fresh allocation ops. E053 already rejects every
//     allocating construct, so any op found here is checker/IR drift (a
//     construct E053 believed heap-neutral that lowers to an alloc) and
//     is deliberately surfaced as an E068 error rather than papered over.
//   - `fbip`     — every constructor allocation site must be reuse-PAIRED
//     with a donor box: the general pairing (computeReuseSources), the
//     self-overwrite hooks (tryStructReuseOverwrite /
//     tryEnumReuseOverwrite), or the consuming-match hand-off
//     (consumingMatchReuse). A paired site lowers to `__alloc_reuse`
//     (an OpCallDirect) instead of OpAlloc, so the op scan below counts
//     exactly the UN-paired fresh sites.
//   - `fip(n)` / `fbip(n)` — at most n fresh (un-paired) constructor
//     allocations are permitted; the count is owned here, not by the
//     checker.
//
// Koka semantics note: fip/fbip is a SHAPE guarantee, not a per-execution
// one. A paired site still carries the runtime `is_unique` guard, and its
// shared-input fallback allocates a fresh box INSIDE the `__alloc_reuse`
// helper — that fallback branch does NOT count as an allocation here
// (exactly Koka's stance: on shared inputs a fip function may copy).
//
// The pass is strictly READ-ONLY over the emitted ops and the rc plan's
// results — it never influences a pairing decision — and it runs on the
// DEFAULT pairing path (plan E3's verdict: `ast.RcReuseDropGuided` stays
// off; when the flag is on, the drop-guided selection is a superset, so
// verification only gets easier).

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// errfCode formats a positioned, coded IR diagnostic as an error value.
// It mirrors the checker's errfCode shape (pos, code, message) so the
// diag catalogue-completeness gate (internal/diag) can scrape this
// package's code emissions, while staying on the IR layer's plain-error
// channel: LowerWith callers already surface the message verbatim.
func errfCode(pos ast.Position, code, format string, args ...any) error {
	return fmt.Errorf("%s: %s: %s", pos, code, fmt.Sprintf(format, args...))
}

// verifyFipAllocs checks a just-lowered `fip` / `fbip` function's ops
// against its allocation budget (see the package comment above). Returns
// an E068 error naming the function, every offending site's position,
// and the count vs the allowance. Called by lowerFunc immediately after
// body emission, before any later pass reshapes the op stream.
func verifyFipAllocs(fn *ast.FuncDecl, out *Func) error {
	if !fn.Fip && !fn.Fbip {
		return nil
	}
	// The verification is defined against the DEFAULT lowering: with the
	// Perceus free/reuse layers force-disabled (debug + differential
	// configurations only), the pairing machinery the fbip credit rests on
	// is deliberately off, so every constructor would read "fresh" and the
	// claim cannot be meaningfully verified — skip instead of mis-reporting.
	if !ast.RcFreeEnabled || !ast.RcReuseEnabled {
		return nil
	}
	labels := ctorSiteLabels(fn)
	var sites []string
	for _, op := range out.Ops {
		var what string
		switch op.Kind {
		case OpAlloc:
			// An OpAlloc in the raw op stream is an UNCONDITIONAL fresh
			// bump allocation at that point of the (possibly branching)
			// program path: every reuse-paired constructor lowers to
			// `__alloc_reuse` instead, and that helper's shared-input
			// fallback allocates internally (exempt by design).
			what = "heap allocation"
			if l, ok := labels[op.Pos]; ok {
				what = l
			}
		case OpStrConcat:
			what = "string concatenation"
		case OpMakeClosure:
			what = "closure construction"
		default:
			continue
		}
		sites = append(sites, fmt.Sprintf("%s at %s", what, op.Pos))
	}
	if len(sites) <= fn.FipAllowance {
		return nil
	}
	kw := "fip"
	if fn.Fbip {
		kw = "fbip"
	}
	remedy := "pair each construction with a dead uniquely-owned donor of the same shape, or grade the claim (`" + kw + "(n)`)"
	if fn.Fip && fn.FipAllowance == 0 {
		// Bare `fip` bodies pass E053 with no allocating construct at all,
		// so a site here means a construct the checker believed
		// heap-neutral lowers to an allocation — surface it as drift.
		remedy = "this construct passed the E053 shape check but lowers to an allocation (checker/IR drift — please report it)"
	}
	return errfCode(fn.P, "E068",
		"`%s` function %q allocates: %d un-reused allocation site(s) exceed the allowance of %d: %s — %s (run `fern explain E068`)",
		kw, fn.Name, len(sites), fn.FipAllowance, strings.Join(sites, "; "), remedy)
}

// ctorSiteLabels maps the source position of every constructor expression
// in the function body to a human-readable label, so an OpAlloc (which
// carries the position the builder stamped at emission) can be reported
// as the construct the user wrote. Best-effort: an alloc emitted for an
// internal shape (an Option rebox, a TRMC loop cell, …) keeps the generic
// "heap allocation" label.
func ctorSiteLabels(fn *ast.FuncDecl) map[ast.Position]string {
	m := map[ast.Position]string{}
	if fn.Body == nil {
		return m
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.StructLit:
			m[x.Pos()] = fmt.Sprintf("struct literal %q", x.TypeName)
		case *ast.TupleLit:
			m[x.Pos()] = "tuple literal"
		case *ast.ArrayLit:
			m[x.Pos()] = "array literal"
		case *ast.Call:
			if x.IsVariantCall {
				m[x.Pos()] = "enum variant construction"
			}
		}
		return true
	})
	return m
}
