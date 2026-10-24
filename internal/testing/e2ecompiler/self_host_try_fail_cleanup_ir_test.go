package e2ecompiler

import (
	"testing"
)

var tryFailCleanupIRCases = []struct {
	name string
	main string
	want int
}{
	{"try-fail-array-reclaimed",
		`function fails(): Option[i32] { return None; }
function step_bare(): Option[i32] { let x: i32 = fails()?; return Some(x); }
function step_owned(): Option[i32] { let owned: i32[] = [1, 2, 3, 4, 5]; let x: i32 = fails()?; return Some(x + owned[0]); }
function main(): i32 {
    let b0: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 20000) { match (step_bare()) { Some(_) => {}, None => {} } i = i + 1; }
    let base: i32 = (__heap_bump_bytes() as i32) - b0;
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 20000) { match (step_owned()) { Some(_) => {}, None => {} } j = j + 1; }
    if ((__heap_bump_bytes() as i32) - b1 - base < 100000) { return 7; }
    return 1;
}`, 7},
	{"try-fail-string-reclaimed",
		`function fails(): Option[i32] { return None; }
function step_bare(): Option[i32] { let x: i32 = fails()?; return Some(x); }
function step_owned(): Option[i32] { let owned: string = "abcdefghijklmnop" + "!"; let x: i32 = fails()?; return Some(x + owned.len()); }
function main(): i32 {
    let b0: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 20000) { match (step_bare()) { Some(_) => {}, None => {} } i = i + 1; }
    let base: i32 = (__heap_bump_bytes() as i32) - b0;
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 20000) { match (step_owned()) { Some(_) => {}, None => {} } j = j + 1; }
    if ((__heap_bump_bytes() as i32) - b1 - base < 100000) { return 7; }
    return 1;
}`, 7},
	{"try-success-value",
		`function ok(): Option[i32] { return Some(41); }
function step(): Option[i32] { let owned: i32[] = [1]; let x: i32 = ok()?; return Some(x + owned[0]); }
function main(): i32 { match (step()) { Some(v) => { return v - 35; }, None => { return 1; } } }`, 7},
}

func TestSelfHostTryFailCleanupIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tryFailCleanupIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
