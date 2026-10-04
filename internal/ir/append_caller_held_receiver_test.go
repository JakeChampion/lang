package ir

import "testing"

// An append on a borrowed array parameter hands back a count of its own (the
// grow helper raises the caller's buffer to rc 2 in place, or returns a fresh
// rc 1 copy), so the binding is reclaimable and its exit release is the
// freeing __fern_arr_dec. While the receiver's borrow taint reached it, the
// release was the flat __fern_rc_dec and the copy leaked on every call
// (#11487).
func TestAppendOnCallerHeldParamIsReclaimable(t *testing.T) {
	for _, tc := range []struct{ name, fn, src string }{
		{"single", "k", `function k(xs: i32[]): i32 {
    let ys: i32[] = xs.append(7);
    return ys.len();
}
function main(): i32 { return k([1, 2]) - 3; }`},
		{"chain", "k", `function k(xs: i32[]): i32 {
    let ys: i32[] = xs.append(7).append(8);
    return ys.len();
}
function main(): i32 { return k([1, 2]) - 4; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dumps := map[string]string{}
			RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
			defer func() { RcPlanHook = nil }()
			p := lowerSourceWith(t, tc.src, 8)
			if !hasPlanName(dumps[tc.fn], "freeEligible", "ys") {
				t.Errorf("ys is not freeEligible — the receiver's borrow taint reached an append result that owns its count; plan:\n%s", dumps[tc.fn])
			}
			if countCalls(p, tc.fn, "__fern_arr_dec") == 0 {
				t.Errorf("%s never releases ys through __fern_arr_dec; ops:\n%s", tc.fn, p)
			}
		})
	}
}

// The credit is for a BORROWED parameter, whose other count is the caller's.
// An `own` parameter is this frame's to release, and here it escapes through a
// raw-pointer cast, so its own release is the flat dec: the result's release
// could then be the last one while the cast address still reads the buffer.
func TestAppendOnEscapedOwnParamKeepsTaint(t *testing.T) {
	src := `function k(own xs: i32[]): i32 {
    let p: usize = xs as usize;
    let ys: i32[] = xs.append(7);
    return ys.len() + (p - p) as i32;
}
function main(): i32 { return k([1, 2]) - 3; }`
	dumps := map[string]string{}
	RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { RcPlanHook = nil }()
	lowerSourceWith(t, src, 8)
	if hasPlanName(dumps["k"], "freeEligible", "ys") {
		t.Errorf("ys is freeEligible although its receiver is an escaped parameter this frame owns; plan:\n%s", dumps["k"])
	}
}
