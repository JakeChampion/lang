package ir

// Native side of the rcPlan differential harness (#4482, goal 2 of the
// #4393/#4474 extraction): render every function's Perceus decision tables
// in a stable, diffable text form, so the self-host port of each analysis
// can be verified TABLE-BY-TABLE against native instead of only end-to-end.
//
// RcPlanHook is consumed in-process (tests / the e2e differential gate set
// it around a LowerWith call); it is nil — and free — in production.
// The format is pinned by TestRcPlanDumpFormat and is the contract the
// self-host dump driver must emit: one `key: value` line per non-empty
// table, name-sets sorted, node-keyed tables rendered as `line:col`
// source positions (stable across both compilers, which parse the same
// source), preciseDrops as `stmtIdx=name+name` pairs sorted by index.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// RcPlanHook, when non-nil, receives (function name, stable rcPlan dump)
// for every function right after lowerFunc finishes it — i.e. with
// preciseDrops filled and every table final.
var RcPlanHook func(fnName, dump string)

func sortedNames(m map[string]bool) string {
	names := make([]string, 0, len(m))
	for n, ok := range m {
		if ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// nodePos renders a table key node as its source position. Both compilers
// parse the same source, so `line:col` is the cross-compiler-stable key for
// AST-node-keyed tables.
func nodePos(n ast.Node) string {
	type positioned interface{ Pos() ast.Position }
	if p, ok := n.(positioned); ok {
		pos := p.Pos()
		return fmt.Sprintf("%d:%d", pos.Line, pos.Col)
	}
	return "?"
}

// dumpRcPlan renders the per-function decision tables. Empty tables are
// omitted so the dump stays small and additions of new tables don't churn
// existing goldens.
func (b *builder) dumpRcPlan() string {
	var sb strings.Builder
	line := func(key, val string) {
		if val != "" {
			fmt.Fprintf(&sb, "%s: %s\n", key, val)
		}
	}
	line("consumedParams", sortedNames(b.rc.consumedParams))
	line("freeEligible", sortedNames(b.rc.freeEligible))
	line("movedLocals", sortedNames(b.rc.movedLocals))

	sites := make([]string, 0, len(b.rc.moveSites))
	for n, ok := range b.rc.moveSites {
		if ok {
			sites = append(sites, nodePos(n))
		}
	}
	sort.Strings(sites)
	line("moveSites", strings.Join(sites, ","))

	incs := make([]string, 0, len(b.rc.arraySetInc))
	for c, inc := range b.rc.arraySetInc {
		incs = append(incs, fmt.Sprintf("%s=%t", nodePos(c), inc))
	}
	sort.Strings(incs)
	line("arraySetInc", strings.Join(incs, ","))

	reuses := make([]string, 0, len(b.rc.reuseSources))
	for c, donor := range b.rc.reuseSources {
		reuses = append(reuses, fmt.Sprintf("%s<-%s", nodePos(c), donor))
	}
	sort.Strings(reuses)
	line("reuseSources", strings.Join(reuses, ","))
	line("reuseConsumed", sortedNames(b.rc.reuseConsumed))

	c2 := make([]string, 0, len(b.rc.consumingMatchReuse))
	for c, ok := range b.rc.consumingMatchReuse {
		if ok {
			c2 = append(c2, nodePos(c))
		}
	}
	sort.Strings(c2)
	line("consumingMatchReuse", strings.Join(c2, ","))

	drops := make([]string, 0, len(b.rc.preciseDrops))
	for idx, names := range b.rc.preciseDrops {
		ns := append([]string(nil), names...)
		sort.Strings(ns)
		drops = append(drops, fmt.Sprintf("%d=%s", idx, strings.Join(ns, "+")))
	}
	sort.Slice(drops, func(i, j int) bool {
		var a, b int
		fmt.Sscanf(drops[i], "%d=", &a)
		fmt.Sscanf(drops[j], "%d=", &b)
		return a < b
	})
	line("preciseDrops", strings.Join(drops, ","))

	// Nested-block precise drops key on the statement to drop after rather than
	// a top-level index, so they render as nodePos "line:col" (like moveSites).
	// Native-only for now — the self-host's irlower has no counterpart, so the
	// differential harness ignores this line as a documented port gap.
	nested := make([]string, 0, len(b.rc.nestedDrops))
	for st, names := range b.rc.nestedDrops {
		ns := append([]string(nil), names...)
		sort.Strings(ns)
		nested = append(nested, fmt.Sprintf("%s=%s", nodePos(st), strings.Join(ns, "+")))
	}
	sort.Strings(nested)
	line("nestedDrops", strings.Join(nested, ","))
	return sb.String()
}
