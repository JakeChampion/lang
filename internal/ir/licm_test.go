package ir

import "testing"

// loopSpan returns the ops strictly inside the first loop of fn.
func loopSpan(t *testing.T, fn *Func) []Op {
	t.Helper()
	for i, o := range fn.Ops {
		if o.Kind != OpLoop {
			continue
		}
		end := matchingScopeEnd(fn.Ops, i)
		if end < 0 {
			t.Fatalf("loop at %d has no matching end", i)
		}
		return fn.Ops[i+1 : end]
	}
	t.Fatal("no loop in function")
	return nil
}

func countKind(ops []Op, k OpKind) int {
	n := 0
	for _, o := range ops {
		if o.Kind == k {
			n++
		}
	}
	return n
}

// `while (i < s.len())` re-reads the length every iteration, and the read is
// a tag test plus two arms because a string may be inline or heap. The
// condition sits in the loop header, so the read happens whenever the loop is
// reached and hoisting it changes nothing observable.
func TestHoistLoopInvariantsLiftsALengthOutOfAWhileCondition(t *testing.T) {
	const src = `
function scan(s: string): i32 {
	var i: i32 = 0;
	var n: i32 = 0;
	while (i < s.len()) {
		if (s[i] == b'#') { n = n + 1; }
		i = i + 1;
	}
	return n;
}`
	p := lowerSource(t, src)
	fn := findFunc(p, "scan")
	if got := countKind(loopSpan(t, fn), OpStrLen); got != 1 {
		t.Fatalf("before the pass the loop should hold the one str.len, got %d:\n%s", got, p)
	}
	HoistLoopInvariants(p)
	fn = findFunc(p, "scan")
	if got := countKind(loopSpan(t, fn), OpStrLen); got != 0 {
		t.Errorf("str.len still inside the loop %d time(s):\n%s", got, p)
	}
	if got := countKind(fn.Ops, OpStrLen); got != 1 {
		t.Errorf("the length should be read exactly once, before the loop; read %d times:\n%s", got, p)
	}
}

// The operand is stored to inside the body, so its length is NOT invariant.
// Hoisting would read the old string's length for every iteration.
func TestHoistLoopInvariantsLeavesAMutatedOperandAlone(t *testing.T) {
	const src = `
function grow(a: string): i32 {
	var s: string = a;
	var i: i32 = 0;
	while (i < s.len()) {
		if (i == 0) { s = a + "x"; }
		i = i + 1;
	}
	return i;
}`
	p := lowerSource(t, src)
	HoistLoopInvariants(p)
	fn := findFunc(p, "grow")
	if got := countKind(loopSpan(t, fn), OpStrLen); got == 0 {
		t.Errorf("the length was hoisted out of a loop that reassigns the string:\n%s", p)
	}
}

// A length read from the BODY rather than the header is not hoisted, and that
// is the safety boundary rather than a missed opportunity: a loop whose body
// never runs would gain a read the original program never performed.
func TestHoistLoopInvariantsLeavesTheBodyAlone(t *testing.T) {
	const src = `
function total(s: string, k: i32): i32 {
	var i: i32 = 0;
	var n: i32 = 0;
	while (i < k) {
		n = n + s.len();
		i = i + 1;
	}
	return n;
}`
	p := lowerSource(t, src)
	HoistLoopInvariants(p)
	fn := findFunc(p, "total")
	if got := countKind(loopSpan(t, fn), OpStrLen); got != 1 {
		t.Errorf("a body-only length must stay in the loop, found %d inside:\n%s", got, p)
	}
}

// Running the pass twice must not hoist the hoist.
func TestHoistLoopInvariantsIsIdempotent(t *testing.T) {
	const src = `
function scan(s: string): i32 {
	var i: i32 = 0;
	while (i < s.len()) { i = i + 1; }
	return i;
}`
	p := lowerSource(t, src)
	HoistLoopInvariants(p)
	once := append([]Op(nil), findFunc(p, "scan").Ops...)
	HoistLoopInvariants(p)
	twice := findFunc(p, "scan").Ops
	if !opsEqual(once, twice) {
		t.Errorf("second run changed the ops:\n%s", p)
	}
}
