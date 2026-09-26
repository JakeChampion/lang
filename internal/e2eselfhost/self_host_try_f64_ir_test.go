package e2eselfhost

import "testing"

// tryF64IRCases pin the `?` (try) operator on an f64 success payload on the
// self-host IR path (follow-up to #3810's string/enum case). f64 is an 8-byte
// payload read through op_opt_payload_w(64) — the same width-64 read the i64/u64
// payload uses — with the `var x: f64 = inner?` binding typing x's slot as f64
// from its annotation. Each case is value-pinned against the native
// interpreter oracle.
var tryF64IRCases = []struct {
	name string
	src  string
	want int
}{
	// `?` on Result[f64, string]: unwrap, `x * 2.0` then cast. 3.5*2 → 7.
	{"result_f64", `function mk(n: i32): Result[f64, string] { if (n > 0) { return Ok(3.5); } return Err("no"); }
function run(n: i32): Result[i32, string] { var x: f64 = mk(n)?; return Ok((x * 2.0) as i32); }
function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 7},
	// `?` on Option[f64]: unwrap, cast. 4.5 → 4.
	{"option_f64", `function find(n: i32): Option[f64] { if (n > 0) { return Some(4.5); } return None; }
function run(n: i32): Option[i32] { var x: f64 = find(n)?; return Some(x as i32); }
function main(): i32 { match (run(1)) { Some(v) => { return v; }, None => { return 0; } } }`, 4},
	// the Err short-circuit of an f64-payload `?`: run(0) → Err → outer Err → 42.
	{"result_f64_err", `function mk(n: i32): Result[f64, string] { if (n > 0) { return Ok(3.5); } return Err("no"); }
function run(n: i32): Result[i32, string] { var x: f64 = mk(n)?; return Ok((x * 2.0) as i32); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	// the None short-circuit of an f64-payload `?`: find(0) → None → 9.
	{"option_f64_none", `function find(n: i32): Option[f64] { if (n > 0) { return Some(4.5); } return None; }
function run(n: i32): Option[i32] { var x: f64 = find(n)?; return Some(x as i32); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 9; } } }`, 9},
}

// TestSelfHostTryF64IR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostTryF64IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tryF64IRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
