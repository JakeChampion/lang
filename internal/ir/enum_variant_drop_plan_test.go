package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

func enumDeclFor(t *testing.T, src, name string) *checker.Info {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if info.Enums[name] == nil {
		t.Fatalf("enum %s not in info", name)
	}
	return info
}

// A payloadless variant is normally the static sentinel, which the
// is_unique gate declines — but the pair-form rebox materialises a real
// rc=1 box for whatever tag the callee returned, so the plan must give
// the payloadless tag an arm that frees the box, at the enum's uniform
// size (the rebox's own layout). Without it a unique None box fell
// through the tag switch unfreed: 32 B per None-returning call (#7732).
func TestEnumVariantDropPlanFreesAPayloadlessBox(t *testing.T) {
	info := enumDeclFor(t, `
enum E { Full(i32[]), Empty }
function main(): i32 { var e: E = E.Full([1]); match (e) { Full(a) => { return a.len(); }, Empty => { return 0; } } return 0; }
`, "E")
	plan, ok := enumVariantDropPlan(info.Enums["E"], 8, true)
	if !ok {
		t.Fatal("no plan for an enum with a droppable payload variant")
	}
	var full, empty *variantDrop
	for i := range plan {
		switch plan[i].tag {
		case 0:
			full = &plan[i]
		case 1:
			empty = &plan[i]
		}
	}
	if full == nil || len(full.loads) == 0 {
		t.Fatal("the payload variant lost its loads — the plan changed shape, not just membership")
	}
	if empty == nil {
		t.Fatal("the payloadless variant has no arm — a unique reboxed Empty falls " +
			"through the tag switch and its box is never freed (#7732)")
	}
	if len(empty.loads) != 0 {
		t.Errorf("the payloadless arm carries %d loads, want none — there is no payload to drop", len(empty.loads))
	}
	if empty.size != full.size {
		t.Errorf("payloadless arm frees %d bytes, payload arm %d — the rebox lays every "+
			"tag out at the uniform size, so the frees must agree or the freelist is fed "+
			"a wrong size class", empty.size, full.size)
	}
}

// An enum with no payload-carrying variant still gets no plan: every
// value is a sentinel, there is no rebox producer for it on the drop
// paths that consult the plan, and generating drop thunks for scalar
// enums would be pure churn.
func TestEnumVariantDropPlanStillDeclinesAnAllPayloadlessEnum(t *testing.T) {
	info := enumDeclFor(t, `
enum Flag { On, Off }
function main(): i32 { var f: Flag = Flag.On; match (f) { On => { return 1; }, Off => { return 0; } } return 0; }
`, "Flag")
	if plan, ok := enumVariantDropPlan(info.Enums["Flag"], 8, true); ok {
		t.Errorf("an all-payloadless enum produced a plan (%d arms) — its values are "+
			"sentinels and the success condition is mirrored elsewhere", len(plan))
	}
}
