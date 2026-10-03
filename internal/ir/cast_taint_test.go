package ir

import "testing"

// A cast is tainted by what it can hold. A scalar result holds nothing, so a
// call whose only other input is a scalar cast still returns something this
// frame owns. A raw address made into a reference (`p as string`) borrows the
// buffer it points at: core/map's __map_string_column builds each key that
// way, and treating the result as owned released every key back out of the
// map, which the SSA backends then read freed (cow_snapshots).
func TestCastTaintFollowsWhatTheCastHolds(t *testing.T) {
	for _, c := range []struct {
		name, fn, local string
		free            bool
		src             string
	}{
		{
			name: "address made into a reference", fn: "view", local: "s", free: false,
			src: `function view(p: usize): i32 {
    let s: string = p as string;
    return s.len();
}
function main(): i32 {
    let t: string = "a string long enough to defeat the small-string optimisation" + "!";
    return view(t as usize) % 7;
}`,
		},
		{
			name: "fresh result beside a scalar cast", fn: "main", local: "s", free: true,
			src: `function mk(n: i64): string {
    if (n > 0i64) { return "a string long enough to defeat" + " the small-string optimisation"; }
    return "z";
}
function main(): i32 {
    let k: i32 = 3;
    let s: string = mk(k as i64);
    return s.len();
}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dumps := map[string]string{}
			RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
			defer func() { RcPlanHook = nil }()
			lowerSourceWith(t, c.src, 8)
			if got := hasPlanName(dumps[c.fn], "freeEligible", c.local); got != c.free {
				t.Errorf("%s freeEligible in %s = %v, want %v; plan:\n%s", c.local, c.fn, got, c.free, dumps[c.fn])
			}
		})
	}
}
