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

// `b = a` where the list declaring `a` ends without mentioning it again
// moves `a` too, including from an if/else arm of that list. A loop body
// between the declaration and the copy re-reads `a` on its next pass, and
// a borrowed view of `a` still reads it, so both keep the copy's retain.
func TestScopeDeadMoveDropsTheCopysRetain(t *testing.T) {
	src := `function tail(s: string): i32 {
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var a: string = s + "x";
        b = a;
        n = n + b.len();
        i = i + 1;
    }
    return n + b.len();
}
function readAfter(s: string): i32 {
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var a: string = s + "x";
        b = a;
        n = n + b.len() + a.len();
        i = i + 1;
    }
    return n + b.len();
}
function arm(s: string, c: boolean): i32 {
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var a: string = s + "x";
        if (c) {
            b = a;
        } else {
            n = n + 1;
        }
        n = n + b.len();
        i = i + 1;
    }
    return n + b.len();
}
function armReadAfter(s: string, c: boolean): i32 {
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var a: string = s + "x";
        if (c) {
            b = a;
        } else {
            n = n + 1;
        }
        n = n + b.len() + a.len();
        i = i + 1;
    }
    return n + b.len();
}
function inner(s: string): i32 {
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var a: string = s + "x";
        var j: i32 = 0;
        while (j < 2) {
            b = a;
            j = j + 1;
        }
        n = n + b.len();
        i = i + 1;
    }
    return n + b.len();
}
function innerRead(s: string): i32 {
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var a: string = s + "x";
        var j: i32 = 0;
        while (j < 2) {
            b = a;
            j = j + a.len();
        }
        n = n + b.len();
        i = i + 1;
    }
    return n + b.len();
}
function viewed(s: string): i32 {
    var b: string = "";
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var a: string = s + "x";
        var v: string = a;
        b = a;
        b = s + "y";
        n = n + b.len() + v.len();
        i = i + 1;
    }
    return n + b.len();
}
function main(): i32 {
    return tail("a") + readAfter("a") + arm("a", true) + armReadAfter("a", true) + inner("a") + innerRead("a") + viewed("a");
}`
	prog := lowerSourceWith(t, src, 8)
	pairs := []struct{ moved, kept, why string }{
		{"tail", "readAfter", "a read later in the declaring list keeps the copy"},
		{"arm", "armReadAfter", "a read after the if in the declaring list keeps the copy"},
	}
	for _, p := range pairs {
		m, k := countRcIncs(prog, p.moved), countRcIncs(prog, p.kept)
		if m != k-1 {
			t.Errorf("%s has %d rc-incs, %s %d: want exactly one fewer (%s)", p.moved, m, p.kept, k, p.why)
		}
	}
	if i, k := countRcIncs(prog, "inner"), countRcIncs(prog, "innerRead"); i != k {
		t.Errorf("inner has %d rc-incs, innerRead %d: want the same, the inner loop re-reads the source", i, k)
	}
	// v borrows a without a count; moving a into b would let b's next
	// write free the box v still reads.
	if v, k := countRcIncs(prog, "viewed"), countRcIncs(prog, "readAfter"); v != k {
		t.Errorf("viewed has %d rc-incs, readAfter %d: want the same, a borrowed source keeps the copy", v, k)
	}
}
