package e2eselfhost

import "testing"

// tryStrEnumIRCases pin the `?` (try) operator on STRING and ENUM success
// payloads on the self-host IR path (#3810, follow-up to #3802's struct case).
// Both are pointer-width payloads handled exactly like the struct case:
// op_opt_payload loads the box pointer, and the `var x: T = inner?` binding types
// x's slot from its annotation (is_str for a string, is_enum_like_name→struct_ty
// for an enum). Each case is value-pinned against the native interpreter oracle.
var tryStrEnumIRCases = []struct {
	name string
	src  string
	want int
}{
	// `?` on Result[string, string]: unwrap, read .len(). "yes" → 3.
	{"result_string", `function mk(n: i32): Result[string, string] { if (n > 0) { return Ok("yes"); } return Err("no"); }
function run(n: i32): Result[i32, string] { var s: string = mk(n)?; return Ok(s.len()); }
function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 3},
	// `?` on Option[string]: unwrap, read .len(). "abcd" → 4.
	{"option_string", `function find(n: i32): Option[string] { if (n > 0) { return Some("abcd"); } return None; }
function run(n: i32): Option[i32] { var s: string = find(n)?; return Some(s.len()); }
function main(): i32 { match (run(1)) { Some(v) => { return v; }, None => { return 0; } } }`, 4},
	// `?` on Result[Enum, string]: unwrap the enum, then match it. Add(8) → 8.
	{"result_enum", `enum Cmd { Add(i32), Sub(i32) }
function mk(n: i32): Result[Cmd, string] { if (n > 0) { return Ok(Add(n)); } return Err("x"); }
function ev(c: Cmd): i32 { match (c) { Add(v) => { return v; }, Sub(v) => { return 0 - v; } } }
function run(n: i32): Result[i32, string] { var c: Cmd = mk(n)?; return Ok(ev(c)); }
function main(): i32 { match (run(8)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 8},
	// the Err short-circuit of a string-payload `?`: run(0) → Err → outer Err → 42.
	{"result_string_err", `function mk(n: i32): Result[string, string] { if (n > 0) { return Ok("yes"); } return Err("no"); }
function run(n: i32): Result[i32, string] { var s: string = mk(n)?; return Ok(s.len()); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	// the None short-circuit of an enum-payload `?`: find(0) → None → 7.
	{"option_enum_none", `enum Cmd { Add(i32), Sub(i32) }
function find(n: i32): Option[Cmd] { if (n > 0) { return Some(Add(n)); } return None; }
function ev(c: Cmd): i32 { match (c) { Add(v) => { return v; }, Sub(v) => { return 0 - v; } } }
function run(n: i32): Option[i32] { var c: Cmd = find(n)?; return Some(ev(c)); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
}

// TestSelfHostTryStrEnumIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostTryStrEnumIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tryStrEnumIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
