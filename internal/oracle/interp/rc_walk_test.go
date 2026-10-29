package interp

import (
	"fmt"
	"testing"
)

func TestArrayBindingWalksOnce(t *testing.T) {
	const n, calls = 4000, 1000
	src := fmt.Sprintf(`function peek(a: i32[], i: i32): i32 {
		let b: i32[] = a;
		return b[i];
	}
	function main(): i32 {
		let a: i32[] = [];
		let i: i32 = 0;
		while (i < %d) { a = a.append(i); i = i + 1; }
		let t: i32 = 0;
		let j: i32 = 0;
		while (j < %d) { t = t + peek(a, j); a = a.with(j, 0); j = j + 1; }
		return t;
	}`, n, calls)
	before := rcWalked.Load()
	v, ip := evalProgram(t, src)
	walked := rcWalked.Load() - before
	if v != Number(calls*(calls-1)/2) {
		t.Fatalf("got %v, want %d", v, calls*(calls-1)/2)
	}
	if walked > 3*n {
		t.Errorf("walked %d elements, want at most %d", walked, 3*n)
	}
	if ip.arraySetCopies != 0 {
		t.Errorf("copied %d elements, want 0", ip.arraySetCopies)
	}
}

func TestScalarEnumListSkipsWalk(t *testing.T) {
	const n = 4000
	src := fmt.Sprintf(`enum L { Nil, Cons(i32, L) }
	function main(): i32 {
		let l: L = Nil;
		let i: i32 = 0;
		while (i < %d) { l = Cons(i, l); i = i + 1; }
		match (l) { Cons(h, t) => { return h; }, Nil => { return 0 - 1; } }
	}`, n)
	before := rcWalked.Load()
	v, _ := evalProgram(t, src)
	walked := rcWalked.Load() - before
	if v != Number(n-1) {
		t.Fatalf("got %v, want %d", v, n-1)
	}
	if walked > n {
		t.Errorf("walked %d payloads, want at most %d", walked, n)
	}
}

func TestEnumCarryingMapIsCounted(t *testing.T) {
	src := `import "core/map";
	enum Box { Full(Map[string, i32]), Empty }
	function main(): i32 {
		let m: Map[string, i32] = map_new(4);
		m = m.insert("a", 1);
		let b: Box = Full(m);
		m = m.insert("a", 2);
		match (b) { Full(inner) => { return inner.get_or("a", 0) * 10 + m.get_or("a", 0); }, Empty => { return 0; } }
	}`
	v, _ := evalProgram(t, src)
	if v != Number(12) {
		t.Fatalf("got %v, want 12: the enum's map changed when the alias was written", v)
	}
}

func TestNestedArraysWalkOnce(t *testing.T) {
	const rows, width, calls = 200, 20, 2000
	src := fmt.Sprintf(`function count(rows: i32[][]): i32 {
		let r: i32[][] = rows;
		return r.len();
	}
	function main(): i32 {
		let rows: i32[][] = [];
		let i: i32 = 0;
		while (i < %d) {
			let row: i32[] = [];
			while (row.len() < %d) { row = row.append(i); }
			rows = rows.append(row);
			i = i + 1;
		}
		let t: i32 = 0;
		let j: i32 = 0;
		while (j < %d) { t = t + count(rows); j = j + 1; }
		return t;
	}`, rows, width, calls)
	before := rcWalked.Load()
	v, _ := evalProgram(t, src)
	walked := rcWalked.Load() - before
	if v != Number(rows*calls) {
		t.Fatalf("got %v, want %d", v, rows*calls)
	}
	if walked > 2*rows*width {
		t.Errorf("walked %d elements, want at most %d", walked, 2*rows*width)
	}
}

func TestArrayAndStructCarryingMapAreCounted(t *testing.T) {
	src := `import "core/map";
	struct Box { m: Map[string, i32] }
	function main(): i32 {
		let m: Map[string, i32] = map_new(4);
		m = m.insert("a", 1);
		let ms: Map[string, i32][] = [map_new(4)];
		ms = ms.with(0, m);
		let b: Box = Box { m: m };
		m = m.insert("a", 2);
		return ms[0].get_or("a", 0) * 100 + b.m.get_or("a", 0) * 10 + m.get_or("a", 0);
	}`
	v, _ := evalProgram(t, src)
	if v != Number(112) {
		t.Fatalf("got %v, want 112: a held map changed when the alias was written", v)
	}
}
