package ir

import "testing"

// A `.with` whose index or value argument READS the receiver — the
// increment-in-place shape `a.with(i, a[i] + 1)` — must not pay a copy for
// it: emitArraySet lowers the arguments before the receiver, so the reads
// are over before the store. The receiver's only later occurrences being
// those reads makes it as dead at the store as a textually last use.
const withReceiverReadInArgsSrc = `function bump(own a: u8[]): u8[] {
    return a.with(0, (a[0] as i32 + 1) as u8);
}
function bumpLen(own a: u8[]): u8[] {
    return a.with(a.len() - 1, a[a.len() - 1]);
}
function stillLive(own a: u8[]): i32 {
    var b: u8[] = a.with(0, (a[0] as i32 + 1) as u8);
    return a[0] as i32 + b[0] as i32;
}
function main(): i32 { return 0; }`

func TestWithReceiverReadInArgsTakesTheInPlacePath(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, withReceiverReadInArgsSrc, ptrW)
		for _, name := range []string{"bump", "bumpLen"} {
			fn := findFunc(p, name)
			if fn == nil {
				t.Fatalf("ptrW=%d: no %s in the lowered program", ptrW, name)
			}
			sites := cowCallSites(fn.Ops)
			if len(sites) != 1 {
				t.Fatalf("ptrW=%d: %s has %d CoW calls, want 1; ops:\n%s", ptrW, name, len(sites), p)
			}
			if !onCopyArm(fn.Ops, sites[0]) {
				t.Errorf("ptrW=%d: %s's `.with` reaches the CoW helper unguarded — the receiver "+
					"read inside its own arguments is being counted as a live use; ops:\n%s", ptrW, name, p)
			}
		}
		fn := findFunc(p, "stillLive")
		if fn == nil {
			t.Fatalf("ptrW=%d: no stillLive in the lowered program", ptrW)
		}
		sites := cowCallSites(fn.Ops)
		if len(sites) != 1 || onCopyArm(fn.Ops, sites[0]) {
			t.Errorf("ptrW=%d: stillLive reads the receiver after the `.with`, so it must keep the "+
				"unguarded copy call; ops:\n%s", ptrW, p)
		}
	}
}
