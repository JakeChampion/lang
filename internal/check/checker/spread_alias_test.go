package checker

import "testing"

const spreadAliasPrefix = `struct State { out: Writer, n: i32, name: string }
function bump(own s: State, by: i32): State { return State { ...s, n: s.n + by }; }
`

// A local made by `T { ...src, f: v }` takes src when v reads only the
// fields of src that the literal replaces.
func TestSpreadAliasReadsReplacedFields(t *testing.T) {
	wantOK(t, "replaced field read", spreadAliasPrefix+`function step(own s0: State, k: i32): State {
  let s = State { ...s0, n: s0.n + 1 };
  if (k == 1) { return bump(s, 10); }
  return bump(s, 100);
}`)
	wantOK(t, "two replaced fields", spreadAliasPrefix+`function step(own s0: State, k: i32): State {
  let s = State { ...s0, n: s0.n + 1, name: s0.name + "!" };
  if (k == 1) { return bump(s, 10); }
  return bump(s, 100);
}`)
}

// A field value can borrow or copy any part of src before the literal takes
// src. A field value that takes a part of src leaves the local borrowed.
func TestSpreadAliasBorrowsTheSource(t *testing.T) {
	step := func(value string) string {
		return `struct P { out: Writer, n: i32, k: i32, name: string, tag: string }
function size(p: P): i32 { return p.n; }
function count(name: string): i32 { return name.len() as i32; }
function keep(own name: string): i32 { return name.len() as i32; }
function take(own p: P): i32 { return p.n; }
function lend(p: P): Writer { return p.out; }
function step(own p0: P): i32 {
  let p = P { ...p0, ` + value + ` };
  return take(p);
}`
	}
	for _, c := range []struct{ name, value string }{
		{"kept field method receiver", `n: p0.name.len() as i32`},
		{"kept field borrow argument", `n: count(p0.name)`},
		{"kept field operand", `tag: p0.name + "!"`},
		{"kept scalar copy", `n: p0.k`},
		{"whole source borrow argument", `n: size(p0)`},
		{"replaced field beside a kept read", `n: p0.n + count(p0.name)`},
		{"kept element operand", `n: if (p0.name[0] == 97u8) { 1 } else { 2 }`},
	} {
		wantOK(t, c.name, step(c.value))
	}
	for _, c := range []struct{ name, value string }{
		{"kept field to an own parameter", `n: keep(p0.name)`},
		{"whole source to an own parameter", `n: take(p0)`},
		{"kept field stored in a replaced field", `tag: p0.name`},
		{"whole source in an array", `n: [p0].len() as i32`},
		{"source in a lambda", `n: (() => p0.n)()`},
		{"borrowing call stored", `out: lend(p0)`},
	} {
		wantE051(t, c.name, step(c.value))
	}
}
