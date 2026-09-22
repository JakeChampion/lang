package ir

import "testing"

// An in-loop `buf = buf.with(i, v)` on a buffer the body owns pays the
// uniqueness test once, before the loop, and the loop body keeps only the
// bounds-checked store.
func TestHoistUniquenessGuardOutOfAlwaysWritingLoop(t *testing.T) {
	p := optimised(t, `@noinline function fill(chunk: string, t: u8[]): string {
  var n: i32 = chunk.len();
  var buf: u8[] = __alloc_u8(n);
  var i: i32 = 0;
  while (i < n) {
    buf = buf.with(i, t[chunk[i] as i32]);
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}
function main(): i32 { var t: u8[] = __alloc_u8(256); return fill("hi", t).len(); }`)
	fn := findFunc(p, "fill")
	if fn == nil {
		t.Fatal("no fill")
	}
	if n := countOps(fn, OpRcIsUnique); n != 1 {
		t.Fatalf("fill has %d uniqueness tests, want 1:\n%s", n, p)
	}
	if !guardPrecedesLoop(fn) {
		t.Errorf("the uniqueness test still sits inside the loop:\n%s", p)
	}
}

// A write under a condition may never run, so its guard stays where the
// write is, behind a flag the loop clears and the first write sets.
func TestHoistUniquenessTestKeepsConditionalCopyAtTheSite(t *testing.T) {
	p := optimised(t, `@noinline function keep(chunk: string, del: u8[]): string {
  var n: i32 = chunk.len();
  var buf: u8[] = __alloc_u8(n);
  var j: i32 = 0;
  var i: i32 = 0;
  while (i < n) {
    var c: i32 = chunk[i] as i32;
    if (del[c] as i32 == 0) {
      buf = buf.with(j, c as u8);
      j = j + 1;
    }
    i = i + 1;
  }
  return slice_unchecked(string_from_bytes_unchecked(buf), 0, j) + "";
}
function main(): i32 { var d: u8[] = __alloc_u8(256); return keep("hi", d).len(); }`)
	fn := findFunc(p, "keep")
	if n := countOps(fn, OpRcIsUnique); n != 1 {
		t.Fatalf("keep has %d uniqueness tests, want 1:\n%s", n, p)
	}
	if guardPrecedesLoop(fn) {
		t.Errorf("a conditional write's uniqueness test left the loop:\n%s", p)
	}
	// The test sits under `if !flag`, and the arm that runs it sets the flag.
	if !guardBehindFlag(fn) {
		t.Fatalf("the uniqueness test is not behind a flag:\n%s", p)
	}
	g := firstOp(fn, OpRcIsUnique)
	flag := fn.Ops[g-4].I32
	loop := firstOp(fn, OpLoop)
	cleared := false
	for i := 0; i+1 < loop; i++ {
		if fn.Ops[i].Kind == OpConstI32 && fn.Ops[i].I32 == 0 && fn.Ops[i+1].Kind == OpStoreLocal && fn.Ops[i+1].I32 == flag {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("the flag is not cleared before the loop:\n%s", p)
	}
	set := false
	for i := g; i+1 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == OpConstI32 && fn.Ops[i].I32 == 1 && fn.Ops[i+1].Kind == OpStoreLocal && fn.Ops[i+1].I32 == flag {
			set = true
		}
	}
	if !set {
		t.Errorf("the flag is not set once the test has run:\n%s", p)
	}
}

// A body that hands the buffer to a call could keep a second reference to
// it, so nothing moves.
func TestHoistUniquenessGuardLeavesAnEscapingReceiver(t *testing.T) {
	p := optimised(t, `@noinline function sink(b: u8[]): i32 { return b.len(); }
@noinline function fill(n: i32): u8[] {
  var buf: u8[] = __alloc_u8(n);
  var i: i32 = 0;
  while (i < n) {
    buf = buf.with(i, 7 as u8);
    i = i + sink(buf);
  }
  return buf;
}
function main(): i32 { return fill(3).len(); }`)
	fn := findFunc(p, "fill")
	if guardPrecedesLoop(fn) {
		t.Errorf("a receiver passed to a call inside the loop had its guard hoisted:\n%s", p)
	}
}

// Element reads and a length read of the receiver keep it owned.
func TestHoistUniquenessGuardAllowsReadsOfTheReceiver(t *testing.T) {
	p := optimised(t, `@noinline function bump(n: i32): u8[] {
  var buf: u8[] = __alloc_u8(n);
  var i: i32 = 0;
  while (i < buf.len()) {
    buf = buf.with(i, (buf[i] as i32 + 1) as u8);
    i = i + 1;
  }
  return buf;
}
function main(): i32 { return bump(3).len(); }`)
	fn := findFunc(p, "bump")
	if !guardPrecedesLoop(fn) && !guardBehindFlag(fn) {
		t.Errorf("reads of the receiver kept its guard on every iteration:\n%s", p)
	}
}

// guardBehindFlag reports whether the function's uniqueness test sits
// under an `if !flag`, the lazy form a write past the loop's exit takes.
func guardBehindFlag(fn *Func) bool {
	g := firstOp(fn, OpRcIsUnique)
	return g >= 4 && fn.Ops[g-2].Kind == OpIf && fn.Ops[g-3].Kind == OpNot && fn.Ops[g-4].Kind == OpLoadLocal
}

func guardPrecedesLoop(fn *Func) bool {
	g := firstOp(fn, OpRcIsUnique)
	l := firstOp(fn, OpLoop)
	return g >= 0 && l >= 0 && g < l
}

func firstOp(fn *Func, kind OpKind) int {
	for i, op := range fn.Ops {
		if op.Kind == kind {
			return i
		}
	}
	return -1
}
