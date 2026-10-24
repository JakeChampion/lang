package e2ecompiler

import "testing"

var varShadowIRCases = []struct {
	name string
	src  string
	want int
}{
	{"if_block", `function main(): i32 { let x = 1; if (true) { let x = 10; } return x; }`, 1},
	{"while_body", `function main(): i32 { let x = 1; let i = 0; while (i < 1) { let x = 10; i = i + 1; } return x; }`, 1},
	{"for_body", `function main(): i32 { let x = 5; for i in [1, 2] { let x = 99; } return x; }`, 5},
	{"inner_then_outer", `function main(): i32 { let x = 1; let r = 0; if (true) { let x = 10; r = x; } return r * 10 + x; }`, 101},
	{"outer_after_in_expr", `function main(): i32 { let x = 1; if (true) { let x = 10; } let y = x + 1; return y; }`, 2},
	{"typed_shadow", `function main(): i32 { let x = "hello"; if (true) { let x = 3; } return x.len(); }`, 5},
	{"two_deep", `function main(): i32 { let x = 1; let a = 0; if (true) { let x = 2; a = a + x; if (true) { let x = 3; a = a + x; } a = a + x; } a = a + x; return a; }`, 8},
	{"for_var_shadow", `function main(): i32 { let i = 99; for i in [1, 2, 3] { } return i; }`, 99},
	{"for_ident_shadow", `function main(): i32 { let a = [1, 2, 3]; let x = 50; for x in a { } return x; }`, 50},
	{"match_bind_shadow", `enum E { A(i32) }
function main(): i32 { let n = 5; let e: E = A(10); let r = 0; match (e) { A(n) => { r = n; } } return r + n; }`, 15},
	{"for_then_redeclare", `function main(): i32 { let s = 0; for i in [1, 2, 3] { s = s + i; } let i = 10; return s + i; }`, 16},
}

func TestSelfHostVarShadowIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range varShadowIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
