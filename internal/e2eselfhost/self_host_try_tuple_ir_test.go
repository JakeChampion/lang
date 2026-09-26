package e2eselfhost

import "testing"

// tryTupleIRCases pin the `?` (try) operator on a TUPLE success payload on the
// self-host IR path — the last `?`-payload shape that bailed to AST (after
// #3810's string/enum and #3822's f64). A tuple is pointer-boxed, so the
// success read is the default pointer-width op_opt_payload (exactly like a
// struct); the `var t: (A, B) = inner?` binding types t's slot via
// mark_tuple_elems from its annotation so a later `t.N` resolves each element.
// Each case is value-pinned against the native interpreter oracle.
var tryTupleIRCases = []struct {
	name string
	src  string
	want int
}{
	// `?` on Result[(i32, string), string]: unwrap, read .0 → 7.
	{"tuple_first", `function mk(n: i32): Result[(i32, string), string] { if (n > 0) { return Ok((7, "hi")); } return Err("no"); }
function run(n: i32): Result[i32, string] { var x: (i32, string) = mk(n)?; return Ok(x.0); }
function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 7},
	// `?` on Option[(i32, i32)]: unwrap, sum the elements. (4,5) → 9.
	{"tuple_sum", `function find(n: i32): Option[(i32, i32)] { if (n > 0) { return Some((4, 5)); } return None; }
function run(n: i32): Option[i32] { var p: (i32, i32) = find(n)?; return Some(p.0 + p.1); }
function main(): i32 { match (run(1)) { Some(v) => { return v; }, None => { return 0; } } }`, 9},
	// the Err short-circuit of a tuple-payload `?`: run(0) → Err → outer Err → 42.
	{"tuple_err", `function mk(n: i32): Result[(i32, string), string] { if (n > 0) { return Ok((7, "hi")); } return Err("no"); }
function run(n: i32): Result[i32, string] { var x: (i32, string) = mk(n)?; return Ok(x.0); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	// the None short-circuit of a tuple-payload `?`: find(0) → None → 8.
	{"tuple_none", `function find(n: i32): Option[(i32, i32)] { if (n > 0) { return Some((4, 5)); } return None; }
function run(n: i32): Option[i32] { var p: (i32, i32) = find(n)?; return Some(p.0 + p.1); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 8; } } }`, 8},
}

// TestSelfHostTryTupleIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostTryTupleIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tryTupleIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
