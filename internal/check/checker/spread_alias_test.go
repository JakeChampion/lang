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
  let _ = w.write("x");
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

const litPartPrefix = litFieldPrefix + `struct P { w: Writer, n: i32, name: string }
struct Q { p: P, n: i32 }
struct G[T] { v: T, w: Writer, n: i32 }
function takep(own p: P): i32 { return p.n; }
function takeq(own q: Q): i32 { return q.n; }
function takeg(own g: G[i32]): i32 { return g.n; }
function takegw(own g: G[Writer]): i32 { return g.n; }
`

// A struct literal gets one verdict as the argument of an own parameter and
// as a local that goes there at its last use. A literal in a field value, and
// a base that is not owned, are decided in the same way for both.
func TestLiteralLocalMatchesLiteralArgument(t *testing.T) {
	for _, c := range []struct {
		name, params, lit, callee string
		ok                        bool
	}{
		{"nested literal from an own parameter", `own u: K, own x: H`, `K { ...u, h: H { ...x, n: 1 } }`, "takek", true},
		{"nested literal that reads its base", `own u: K, own x: H`, `K { ...u, h: H { ...x, n: x.n + 1 } }`, "takek", true},
		{"nested literal of own parameters", `own u: K, own w: Writer, own a: Writer`, `K { ...u, h: H { w: w, alt: a, n: 0, name: "" } }`, "takek", true},
		{"nested literal with no base", `own x: Writer, own y: Writer`, `K { h: H { w: x, alt: y, n: 0, name: "" }, n: 1 }`, "takek", true},
		{"borrowed base that keeps no handle", `b: P, own w: Writer`, `P { ...b, w: w }`, "takep", true},
		{"nested borrowed base that keeps no handle", `own u: Q, b: P, own w: Writer`, `Q { ...u, p: P { ...b, w: w } }`, "takeq", true},
		{"generic borrowed base that keeps no handle", `b: G[i32], own w: Writer`, `G { ...b, w: w }`, "takeg", true},
		{"generic borrowed base that keeps a handle", `b: G[Writer], own w: Writer`, `G { ...b, w: w }`, "takegw", false},
		{"nested literal from a borrowed parameter", `own u: K, x: H`, `K { ...u, h: H { ...x, n: 1 } }`, "takek", false},
		{"nested borrowed base that keeps a handle", `own u: K, b: H, own w: Writer`, `K { ...u, h: H { ...b, w: w } }`, "takek", false},
		{"borrowed base that keeps a handle", `b: H, own w: Writer`, `H { ...b, w: w }`, "take", false},
		{"nested borrowed handle field", `own u: K, b: H`, `K { ...u, h: H { w: b.w, alt: stdout(), n: 0, name: "" } }`, "takek", false},
	} {
		local := litPartPrefix + `function f(` + c.params + `): i32 {
  let v = ` + c.lit + `;
  return ` + c.callee + `(v);
}`
		inline := litPartPrefix + `function f(` + c.params + `): i32 {
  return ` + c.callee + `(` + c.lit + `);
}`
		if c.ok {
			wantOK(t, c.name+" (local)", local)
			wantOK(t, c.name+" (argument)", inline)
		} else {
			wantE051(t, c.name+" (local)", local)
			wantE051(t, c.name+" (argument)", inline)
		}
	}
	wantE051(t, "nested base read after the literal", litPartPrefix+`function f(own u: K, own x: H): i32 {
  let v = K { ...u, h: H { ...x, n: 1 } };
  let _ = x.w.write("x");
  return takek(v);
}`)
	wantOK(t, "nested literal of a payload and two locals", `struct I { r: Reader, open: boolean, n: i32 }
struct R { inp: I, n: i32 }
function reset(own r: R): R { return r; }
function g(own r: R, again: boolean): R {
  let inp = I { ...r.inp, n: r.inp.n + 1 };
  let fresh = R { ...r, n: 0 };
  match (open_reader("/dev/null")) {
    Ok(reader) => {
      let opened = R { ...fresh, inp: I { ...inp, r: reader, open: true } };
      if (again) {
        return reset(reset(opened));
      }
      return reset(opened);
    },
    Err(_) => { return fresh; }
  }
}`)
}
