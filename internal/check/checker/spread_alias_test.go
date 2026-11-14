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
	wantE051(t, "kept field read", spreadAliasPrefix+`function step(own s0: State, k: i32): State {
  let s = State { ...s0, n: s0.name.len() };
  if (k == 1) { return bump(s, 10); }
  return bump(s, 100);
}`)
	wantE051(t, "whole source read", spreadAliasPrefix+`function size(s: State): i32 { return s.n; }
function step(own s0: State, k: i32): State {
  let s = State { ...s0, n: size(s0) };
  if (k == 1) { return bump(s, 10); }
  return bump(s, 100);
}`)
}
