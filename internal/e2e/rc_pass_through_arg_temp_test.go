package e2e

import "testing"

// A fresh temp passed to a function that may hand its string parameter back
// unchanged is released by the caller (#11479).
//
// `same` returns its borrowed parameter, which takes the return-transfer inc,
// so whatever `add` keeps of `name` holds a count of its own. The counted-
// retain summary nonetheless refused a bare `return p`, the refusal reached
// `add` through the argument-position rule, and with the position uncredited
// neither side released `fresh(base)`: one string per call. std/headers'
// `append(name.to_lower())` is this shape once `to_lower` returns an
// already-lowercase input as is.
func TestPassThroughParamLeavesTheCallersTempReclaimable(t *testing.T) {
	checkGoCompilerCensusOnEveryTarget(t, []struct{ name, src string }{
		{"forwarded_through_a_pass_through", `
function same(s: string): string {
  return s;
}

function add(names: string[], name: string): string[] {
  return names.append(same(name));
}

function fresh(a: string): string {
  return a + a;
}

function main(): i32 {
  let total: i32 = 0;
  let i: i32 = 0;
  let base: string = "x-echo-header-name-long";
  while (i < 20) {
    let names: string[] = [];
    names = add(names, fresh(base));
    if (names[0] != base + base) { return 1; }
    total = total + names.len();
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 99; }
  if (total != 20) { return 1; }
  return 0;
}`},
	}, "the temp handed to a pass-through helper is released by nobody (#11479)")
}
