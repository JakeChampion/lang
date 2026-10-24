package e2e

import "testing"

func TestPassThroughParamLeavesTheCallersTempReclaimable(t *testing.T) {
	checkCensusOnEveryTarget(t, []struct{ name, src string }{
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
