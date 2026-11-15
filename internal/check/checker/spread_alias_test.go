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

const litFieldPrefix = `struct H { w: Writer, alt: Writer, n: i32, name: string }
struct K { h: H, n: i32 }
function take(own h: H): i32 { return h.n; }
function takek(own k: K): i32 { return k.n; }
`

// A literal local can go to an own parameter only when each field value that
// it sets is owned or holds no handle. A spread base does not change this.
func TestLiteralLocalFieldValuesOwned(t *testing.T) {
	spread := func(params, value string) string {
		return litFieldPrefix + `function f(own u: H, ` + params + `): i32 {
  let v = H { ...u, ` + value + ` };
  return take(v);
}`
	}
	for _, c := range []struct{ name, params, value string }{
		{"fresh handle", `b: H`, `alt: stdout()`},
		{"own parameter handle", `own w: Writer`, `alt: w`},
		{"scalar", `b: H`, `n: b.n`},
		{"counted string copy", `b: H`, `name: b.name`},
		{"string built from a borrow", `b: H`, `name: b.name + "!"`},
	} {
		wantOK(t, "spread "+c.name, spread(c.params, c.value))
	}
	for _, c := range []struct{ name, params, value string }{
		{"borrowed handle field", `b: H`, `alt: b.w`},
		{"borrowed handle parameter", `w: Writer`, `alt: w`},
	} {
		wantE051(t, "spread "+c.name, spread(c.params, c.value))
	}
	wantOK(t, "spread local at its last use", litFieldPrefix+`function f(own u: H, own w: Writer): i32 {
  let x = w;
  let v = H { ...u, alt: x };
  return take(v);
}`)
	wantE051(t, "spread own handle read later", litFieldPrefix+`function f(own u: H, own w: Writer): i32 {
  let v = H { ...u, alt: w };
  w.write_str("x");
  return take(v);
}`)
	wantE051(t, "spread borrowed struct holding a handle", litFieldPrefix+`function f(own u: K, b: H): i32 {
  let v = K { ...u, h: b };
  return takek(v);
}`)
	wantE051(t, "spread nested literal holding a borrow", litFieldPrefix+`function f(own u: K, b: H): i32 {
  let v = K { ...u, h: H { w: b.w, alt: stdout(), n: 0, name: "" } };
  return takek(v);
}`)
	wantE051(t, "spread self-update storing a borrow", litFieldPrefix+`function f(own u: H, b: H): i32 {
  let v = H { ...u, n: 1 };
  v = H { ...v, alt: b.alt };
  return take(v);
}`)

	plain := func(params, value string) string {
		return litFieldPrefix + `function f(` + params + `): i32 {
  let v = H { ` + value + `, n: 0, name: "" };
  return take(v);
}`
	}
	wantOK(t, "plain own parameters", plain(`own x: Writer, own y: Writer`, `w: x, alt: y`))
	wantOK(t, "plain fresh handles", plain(`b: H`, `w: stdout(), alt: stdout()`))
	wantOK(t, "plain own parameter and local", litFieldPrefix+`function f(own x: Writer, own y: Writer): i32 {
  let z = y;
  let v = H { w: x, alt: z, n: 0, name: "" };
  return take(v);
}`)
	wantE051(t, "plain borrowed handle fields", plain(`b: H`, `w: b.w, alt: b.alt`))
	wantE051(t, "plain borrowed handle parameter", plain(`w: Writer, own x: Writer`, `w: w, alt: x`))
	wantE051(t, "plain reassigned from a borrow", litFieldPrefix+`function f(own x: Writer, own y: Writer, b: H): i32 {
  let v = H { w: x, alt: y, n: 0, name: "" };
  v = H { w: b.w, alt: b.alt, n: 0, name: "" };
  return take(v);
}`)
	wantE051(t, "plain nested literal holding a borrow", litFieldPrefix+`function f(b: H): i32 {
  let v = K { h: H { w: b.w, alt: b.alt, n: 0, name: "" }, n: 1 };
  return takek(v);
}`)
}
