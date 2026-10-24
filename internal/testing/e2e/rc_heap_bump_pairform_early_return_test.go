package e2e

import (
	"testing"
)

func pairFormEarlyReturnBody(take string) string {
	return take + `
function main(): i32 {
    let b0: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + take(i % 3); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + take(j % 3); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (acc < 0) { return 97; }
    if ((b2 - b1) > (b1 - b0)) { return 98; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`
}

var pairFormEarlyReturnStr = pairFormEarlyReturnBody(`function tag(v: i32): string { if (v == 0) { return "aa"; } if (v == 1) { return "bb"; } return "cc"; }
function mk(v: i32): Option[string] { if (v < 0) { return None; } return Some("pf-payload-" + tag(v)); }
function take(v: i32): i32 { match (mk(v)) { Some(s) => { return s.len(); }, None => { return 0; } } return 0; }`)

var pairFormEarlyReturnArr = pairFormEarlyReturnBody(`function mk(v: i32): Option[i32[]] { if (v < 0) { return None; } return Some([v, v + 1, v + 2]); }
function take(v: i32): i32 { match (mk(v)) { Some(a) => { return a.len(); }, None => { return 0; } } return 0; }`)

var pairFormEarlyReturnUnused = pairFormEarlyReturnBody(`function mk(v: i32): Option[i32[]] { if (v < 0) { return None; } return Some([v, v + 1, v + 2]); }
function take(v: i32): i32 { match (mk(v)) { Some(a) => { return 1; }, None => { return 0; } } return 0; }`)

const pairFormReturningArmAliasedSafe = `function wrap(s: string): Option[string] { return Some(s); }
function take(s: string): i32 { match (wrap(s)) { Some(t) => { return t.len(); }, None => { return 0; } } return 0; }
function main(): i32 {
    let b: string = "shared-" + "payload";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        acc = acc + take(b);
        i = i + 1;
    }
    if (acc != 2800) { return 99; }
    if (b.len() != 14) { return 88; }
    return __rc_underflow_count();
}`

func checkPairFormReturningArmSafe(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, pairFormReturningArmAliasedSafe); code != 0 {
		t.Errorf("aliased pair-form payload: code=%d (99=value, 88=UAF/freed-early, >0=over-release)", code)
	}
}

var pairFormEarlyReturnNative = []string{pairFormEarlyReturnStr, pairFormEarlyReturnArr, pairFormEarlyReturnUnused}

func TestX86_64PairFormReturningArmReclaim(t *testing.T) {
	for _, src := range pairFormEarlyReturnNative {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("pair-form early-return payload: code=%d (98=grows, 99=over-release, 97=value)", code)
		}
	}
	checkPairFormReturningArmSafe(t, compileAndRunX86_64FreeOn)
}

func TestArm64PairFormReturningArmReclaim(t *testing.T) {
	for _, src := range []string{pairFormEarlyReturnArr, pairFormEarlyReturnUnused} {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("pair-form early-return payload: code=%d (98=grows, 99=over-release, 97=value)", code)
		}
	}
}

func TestWASMPairFormReturningArmReclaim(t *testing.T) {
	for _, src := range []string{pairFormEarlyReturnArr, pairFormEarlyReturnUnused} {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("pair-form early-return payload: code=%d (98=grows, 99=over-release, 97=value)", got)
		}
	}
}
