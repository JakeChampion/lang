package e2e

import "testing"

// `var y = c` over a match binding takes a reference nothing gives back.
//
// A binding is bound WITHOUT an inc — bindingSlotScoped hands the arm a
// borrow — so it is not an owned rc local, and neither the dead-alias leg nor
// the borrowed-parameter leg of computeBorrowedAliases claimed it. The Var
// lowering still emitted the transfer inc, while the exit sweep skipped the
// dec because an alias is never freeEligible: one reference stranded per arm
// execution, which in a drain loop is the whole payload every iteration
// (#9923).
//
// The match-binding leg cancels that inc, on the borrowed-parameter leg's
// reasoning with the SCRUTINEE in the caller's place — it owns the payload
// across the whole arm, and the only release the arm emits is the fresh-call
// reclaim at the join, after the body.
//
// `direct_scrutinee` is the second half. A cancelled var init is now an
// EXCUSED use of the binding (bindingUsesExcused), which it could not be
// while the init took an uncancelled inc — so the fresh-call payload reclaim
// (#8003) applies to an arm that binds through a `var`, where before it was
// refused and the callee's payload was abandoned. Measured on that shape
// alone: 1280 bytes stranded over 50 rounds before, 0 after.
//
// NOT pinned here, and still leaking: an arm alias that escapes into an outer
// POINTER local (`var kept = c; chunk = kept`). The payload half of that
// reclaims with this change, but the destination inherits the alias's
// free-eligibility taint through rhsTainted's Ident arm and loses its own
// drop. That is a second defect with a different cause — computeFreeEligible
// runs BEFORE computeBorrowedAliases and the dead-alias leg reads its result,
// so untainting a counted destination needs the pipeline reordered rather
// than a rule added. Filed as #9948.
func TestMatchBindingAliasIsCancelled(t *testing.T) {
	mk := `
function mk(i: i32): Option[u8[]] {
    var b: u8[] = [];
    var j: i32 = 0;
    while (j < 8) { b = b.append(((j + i) % 251) as u8); j = j + 1; }
    if (i % 5 == 0) { return None; }
    return Some(b);
}
`
	// The binding is aliased into an arm-local `var` that is only READ, so
	// nothing outlives the arm and the cancellation is the whole question.
	boundScrutinee := mk + `
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var r: Option[u8[]] = mk(i);
        match (r) {
            Some(c) => { var kept: u8[] = c; total = total + kept.len(); },
            None => { },
        }
        i = i + 1;
    }
    if (total != 320) { return 91; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

	// The same arm over a DIRECT call scrutinee, where the payload has no
	// owner but the join's reclaim — which the var init used to refuse.
	directScrutinee := mk + `
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        match (mk(i)) {
            Some(c) => { var kept: u8[] = c; total = total + kept.len(); },
            None => { },
        }
        i = i + 1;
    }
    if (total != 320) { return 91; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

	// The control that puts it on the BINDING: the same arm-scoped `var`
	// aliasing an ordinary outer local instead was always balanced, and a
	// leg that started claiming those would show up here.
	outerLocalControl := mk + `
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    var side: u8[] = [1, 2, 3];
    while (i < 50) {
        match (mk(i)) {
            Some(c) => { var kept: u8[] = side; total = total + kept.len() + c.len(); },
            None => { },
        }
        i = i + 1;
    }
    if (total != 440) { return 91; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

	for _, tc := range []struct {
		name string
		run  func(*testing.T, string) (string, string, int)
	}{
		{"x86_64", runLeakCheckX86_64},
		{"arm64", runLeakCheckArm64},
		{"wasm", func(t *testing.T, src string) (string, string, int) {
			return runLeakCheckWasm(t, src, false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, shape := range []struct{ name, src string }{
				{"bound_scrutinee", boundScrutinee},
				{"direct_scrutinee", directScrutinee},
				{"outer_local_control", outerLocalControl},
			} {
				t.Run(shape.name, func(t *testing.T) {
					_, stderr, code := tc.run(t, shape.src)
					if code != 0 {
						t.Fatalf("exit=%d, want 0 — 91 is a wrong total (the cancelled alias "+
							"read the wrong bytes), 99 a non-zero __rc_underflow_count()", code)
					}
					allocs, frees, live := parseLeakCheckLine(t, stderr)
					if allocs == 0 {
						t.Fatalf("no allocations — the loop is not running")
					}
					if allocs != frees || live != 0 {
						t.Errorf("allocs=%d frees=%d live_bytes=%d, want balanced / 0 — the "+
							"arm's `var` takes a reference nothing gives back (#9923)",
							allocs, frees, live)
					}
				})
			}
		})
	}
}
