package e2e

import "testing"

// TestDeferReadLocalKeepsItLive covers #9471: a `defer` whose action reads an
// rc-tracked LOCAL miscompiled on both register backends, where the
// interpreter answered correctly.
//
// preciseDropTarget placed the local's release right after its last
// referencing statement, and counted the `defer` as that reference. But a
// `defer` STATEMENT only registers the action — emitDeferCleanupKind
// re-evaluates the expression at each exit, by which time the release has run
// and zeroed the slot, so the replay read a null. Placing the drop later is not
// the fix: the replay sites are the exits, and the exit sweep is already
// exactly that placement (every exit runs emitDeferCleanup before
// emitRcDecLocalsAtExit), so such a local simply stays on the sweep.
//
// The defect had two faces and the quiet one is the reason these cases pin the
// ANSWER rather than just a clean exit: an indexed read through the zeroed slot
// faults, but `.len()` on it returns 0, so the program ran to completion with a
// wrong result and nothing to notice.
func TestDeferReadLocalKeepsItLive(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// The minimal shape: the defer is the local's ONLY reference, so the
		// release landed immediately after the array was built.
		{"defer_is_only_use", `function f(out: Cell[i32]): i32 {
    var arr: i32[] = [1, 2, 3];
    defer out.set(arr[1]);
    return 0;
}
function main(): i32 { var a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 2},
		// A real use BEFORE the defer and none after — the release still lands
		// on the defer statement, so reading it earlier does not save it.
		{"last_use_before_defer", `function f(out: Cell[i32]): i32 {
    var arr: i32[] = [1, 2, 3];
    out.set(arr[0]);
    defer out.set(arr[1]);
    return 0;
}
function main(): i32 { var a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 2},
		// The quiet face: `.len()` through the zeroed slot answered 0 instead
		// of faulting, so this case fails on the VALUE, not on a crash.
		{"silent_wrong_answer", `function f(out: Cell[i32]): i32 {
    var arr: i32[] = [1, 2, 3];
    defer out.set(arr.len());
    return 0;
}
function main(): i32 { var a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 3},
		// A use AFTER the defer was always correct — the last-use scan picked
		// the later statement, so the local fell through to the exit sweep on
		// its own. It is here so the fix stays a bail-out for defer-read
		// locals rather than a blanket one for any function holding a defer.
		{"use_after_defer_still_precise", `function f(out: Cell[i32]): i32 {
    var arr: i32[] = [1, 2, 3];
    defer out.set(arr[1]);
    return arr[0];
}
function main(): i32 { var a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 2},
		// errdefer replays through emitErrDeferCleanup rather than
		// emitDeferCleanup, so it is a second replay site with the same
		// hazard — and collectDefers gathers both forms into b.defers, so
		// one bail-out covers both. Confirmed failing the same way before
		// the fix.
		{"errdefer_reads_local", `function f(out: Cell[i32]): Option[i32] {
    var arr: i32[] = [1, 2, 3];
    errdefer out.set(arr[1]);
    return None;
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (f(a)) { Some(v) => { return 90; }, None => { return a.get(); } }
}`, 2},
		// The reported program (#9471): a defer inside a loop reading a
		// loop-local array, with a `continue` edge out of a nested block. The
		// loop and the `continue` turned out to be incidental — the nested
		// declaration routes through computeNestedDrops, which shares
		// preciseDropTarget with the top-level pass — but it is the shape that
		// was reported and it belongs in the record.
		{"defer_in_loop_over_loop_local", `function f(out: Cell[i32]): i32 {
    var i: i32 = 0;
    while (i < 4) {
        var arr: i32[] = [i, i + 1, i + 2];
        defer out.set(out.get() + arr[1]);
        if (i == 2) {
            var brr: i32[] = [9, 9];
            out.set(out.get() + brr[0]);
            i = i + 1;
            continue;
        }
        i = i + 1;
    }
    return 0;
}
function main(): i32 { var a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 19},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if code := runInterpExit(t, c.src); code != c.want {
				t.Errorf("interp exit = %d, want %d", code, c.want)
			}
			t.Run("wasm32-wasi", func(t *testing.T) {
				if code := compileAndRunWasmbinMain(t, c.src); code != c.want {
					t.Errorf("wasm exit = %d, want %d", code, c.want)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				if _, code := compileAndRunArm64(t, c.src); code != c.want {
					t.Errorf("arm64 exit = %d, want %d (139 = the zeroed slot read as a pointer)", code, c.want)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != c.want {
					t.Errorf("x86_64 exit = %d, want %d (139 = the zeroed slot read as a pointer)", code, c.want)
				}
			})
		})
	}
}
