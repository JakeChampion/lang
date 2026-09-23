package ir

import "testing"

// `b = a` where `a` is written again before anything reads it moves `a`
// into `b`: the copy's retain goes, and `a`'s slot is emptied so the write
// has nothing to release (computeOverwriteMoves). Each shape is paired with
// a control differing only in what makes the move unsound, which must keep
// the retain — so the pair's difference is exactly the one removed inc.
func TestOverwriteMoveDropsTheCopysRetain(t *testing.T) {
	src := `function plain(s: string): i32 {
    var a: string = s + "x";
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        b = a;
        a = s + "y";
        n = n + b.len();
        i = i + 1;
    }
    return n + a.len();
}
function readFirst(s: string): i32 {
    var a: string = s + "x";
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        b = a;
        n = n + a.len();
        a = s + "y";
        n = n + b.len();
        i = i + 1;
    }
    return n + a.len();
}
function arms(s: string, c: boolean): i32 {
    var a: string = s + "x";
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        b = a;
        if (c) {
            a = s + "y";
        } else {
            a = s + "z";
        }
        n = n + b.len();
        i = i + 1;
    }
    return n + a.len();
}
function oneArm(s: string, c: boolean): i32 {
    var a: string = s + "x";
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        b = a;
        if (c) {
            a = s + "y";
        } else {
            n = n + 1;
        }
        n = n + b.len();
        i = i + 1;
    }
    return n + a.len();
}
function jumps(s: string, c: boolean): i32 {
    var a: string = s + "x";
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        b = a;
        if (c) {
            break;
        } else {
            n = n + 1;
        }
        a = s + "y";
        n = n + b.len();
        i = i + 1;
    }
    return n + a.len();
}
function main(): i32 { return plain("a") + readFirst("a") + arms("a", true) + oneArm("a", true) + jumps("a", true); }`
	prog := lowerSourceWith(t, src, 8)
	pairs := []struct{ moved, kept, why string }{
		{"plain", "readFirst", "a read of the source before its write keeps the copy"},
		{"arms", "oneArm", "a write in one arm only leaves the other path reading the source"},
	}
	for _, p := range pairs {
		m, k := countRcIncs(prog, p.moved), countRcIncs(prog, p.kept)
		if m != k-1 {
			t.Errorf("%s has %d rc-incs, %s %d: want exactly one fewer (%s)", p.moved, m, p.kept, k, p.why)
		}
	}
	// A break between the copy and the write can reach a read of an
	// emptied slot after the loop, so the copy keeps its retain there.
	if j, k := countRcIncs(prog, "jumps"), countRcIncs(prog, "oneArm"); j != k {
		t.Errorf("jumps has %d rc-incs, oneArm %d: want the same, a break must keep the copy", j, k)
	}
}
