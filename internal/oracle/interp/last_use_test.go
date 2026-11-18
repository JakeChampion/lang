package interp

import (
	"fmt"
	"testing"
)

// The decoder shape of std/xz: each bit gives the table to an own parameter
// and takes it back out of a tuple. With n bits and a table of n elements, a
// copy per bit is n*n elements.
const lastUseShapes = `function bit(own p: i32[], i: i32): (i32[], i32) {
	let v = p[i];
	return (p.with(i, v + 1), v & 1);
}

function tree(own probs: i32[], at: i32, n: i32): (i32[], i32) {
	let m = 0;
	for k in 0..n {
		let (p, b) = bit(probs, at + k);
		probs = p;
		m = m + b;
	}
	return (probs, m);
}

function pair(own probs: i32[], at: i32): (i32[], i32) {
	let (p, b) = bit(probs, at);
	if (b == 1) {
		return bit(p, at + 1);
	}
	let (p2, c) = tree(p, at + 1, 2);
	return (p2, c);
}

struct Step { probs: i32[], bit: i32, at: i32 }

function step(own probs: i32[], at: i32): Step {
	let (p, b) = bit(probs, at);
	return Step { probs: p, bit: b, at: at + 1 };
}

function zeros(n: i32): i32[] {
	let a: i32[] = [];
	for _ in 0..n {
		a = a.append(0);
	}
	return a;
}
`

func lastUseCopies(t *testing.T, body string, n int) int {
	t.Helper()
	src := lastUseShapes + fmt.Sprintf(`
function main(): i32 {
	let n = %d;
	let probs = zeros(n);
	%s
	return probs[0];
}`, n, body)
	v, ip := evalProgram(t, src)
	if v != Number(1) {
		t.Fatalf("probs[0] = %v, want 1", v)
	}
	return ip.arraySetCopies
}

// A local at its last use that a call gives to an own parameter moves into
// the call, the names of a tuple that nothing holds take its elements, and a
// read of a field at its last use takes the field out of a struct that only
// its local holds, so each write of the table is in place.
func TestLastUseWritesInPlace(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"loop of bits", `let (p, m) = tree(probs, 0, n);
	probs = p;`},
		{"fields of a struct", `let k = 0;
	while (k < n) {
		let s = step(probs, k);
		probs = s.probs;
		k = s.at + s.bit;
	}`},
		{"calls in a loop", `let k = 0;
	while (k + 3 < n) {
		let (p, b) = pair(probs, k);
		probs = p;
		k = k + 3 + b;
	}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if copies := lastUseCopies(t, tc.body, 600); copies != 0 {
				t.Errorf("copied %d elements, want 0", copies)
			}
			t.Setenv("FERN_INTERP_ARRAY_COW", "verify")
			lastUseCopies(t, tc.body, 600)
		})
	}
}

// A tuple that a local holds keeps its holds when a destructure reads it, so
// a write through one of the names copies, and the local keeps the old
// elements.
func TestHeldTupleKeepsItsElements(t *testing.T) {
	t.Setenv("FERN_INTERP_ARRAY_COW", "verify")
	v, _ := evalProgram(t, lastUseShapes+`
function main(): i32 {
	let x = zeros(2);
	let t = bit(x, 0);
	let (p, b) = t;
	let q = p.with(0, 7);
	let (r, c) = t;
	return r[0] * 100 + q[0] * 10 + b + c;
}`)
	if v != Number(170) {
		t.Errorf("got %v, want 170", v)
	}
}

// A read of a field takes the field out only at its last use, and only from a
// struct that one local holds.
func TestStructFieldStaysWhileRead(t *testing.T) {
	t.Setenv("FERN_INTERP_ARRAY_COW", "verify")
	v, _ := evalProgram(t, lastUseShapes+`
function main(): i32 {
	let s = step(zeros(3), 0);
	let kept = s;
	let a = s.probs;
	let b = a.with(1, 5);
	let c = s.probs;
	let d = kept.probs.with(2, 7);
	return b[1] * 1000 + c[1] * 100 + d[2] * 10 + kept.probs[0];
}`)
	if v != Number(5071) {
		t.Errorf("got %v, want 5071", v)
	}
}
