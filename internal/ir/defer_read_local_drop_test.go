package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// TestDeferReadLocalBailoutIsScoped pins the SHAPE of the #9471 bail-out, which
// the e2e cases cannot: they assert answers, and a local keeping or losing its
// precise drop is not observable in an answer either way.
//
// Both halves matter. A defer-read local must lose its precise drop, or the
// release zeroes the slot before emitDeferCleanupKind replays the expression.
// A local the defer does NOT read must keep its, or the fix is a blanket
// "functions with a defer get no precise drops" — which would cost every such
// function the garbage-free placement the pass exists for.
func TestDeferReadLocalBailoutIsScoped(t *testing.T) {
	dumps := map[string]string{}
	ir.RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { ir.RcPlanHook = nil }()

	// `read` is named by the defer; `other` is not. Both are owned rc locals
	// declared at the top level of the same function, so they differ only in
	// whether the defer names them.
	lowerForTest(t, `function sink(v: i32): i32 { return v; }
function both(): i32 {
	var read: i32[] = [1, 2, 3];
	var other: i32[] = [4, 5, 6];
	defer sink(read[1]);
	var s: i32 = other[0];
	return s;
}
function main(): i32 { return both(); }
`)
	dump := dumps["both"]
	if dump == "" {
		t.Fatal("RcPlanHook never fired for `both`")
	}
	var drops string
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "preciseDrops:") {
			drops = strings.TrimSpace(line)
		}
	}
	if strings.Contains(drops, "read") {
		t.Errorf("`read` is named by the defer and must stay on the exit sweep, "+
			"but it has a precise drop (#9471): %s", drops)
	}
	if !strings.Contains(drops, "other") {
		t.Errorf("`other` is not named by the defer and must keep its precise drop — "+
			"the bail-out is scoped to defer-read locals, not to functions holding a "+
			"defer: %s", drops)
	}
}
