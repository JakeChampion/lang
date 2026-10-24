package e2ecompiler

import "testing"

var deferInLoopCases = []struct {
	name string
	main string
	want int
}{
	{"while_per_iteration", `function f(a: Cell[i32]): i32 { let i: i32 = 0; while (i < 3) { defer a.set(a.get() + 1); i = i + 1; } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); let inside: i32 = f(a); return inside * 10 + a.get(); }`, 33},
	{"for_in_per_iteration", `function f(a: Cell[i32]): i32 { for i in 0..3 { defer a.set(a.get() + 1); } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); let inside: i32 = f(a); return inside * 10 + a.get(); }`, 33},
	{"break_edge", `function f(a: Cell[i32]): i32 { let i: i32 = 0; while (i < 5) { defer a.set(a.get() + 1); if (i == 2) { break; } i = i + 1; } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); let inside: i32 = f(a); return inside * 10 + a.get(); }`, 33},
	{"continue_edge", `function f(a: Cell[i32]): i32 { let i: i32 = 0; let t: i32 = 0; while (i < 4) { defer a.set(a.get() + 1); i = i + 1; if (i == 2) { continue; } t = t + 1; } return t * 10 + a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 34},
	{"labelled_break_unwinds_inner_first", `function f(a: Cell[i32]): i32 { let i: i32 = 0; outer: while (i < 3) { defer a.set(a.get() * 4 + 2); let j: i32 = 0; while (j < 3) { defer a.set(a.get() * 4 + 1); if (j == 1) { break outer; } j = j + 1; } i = i + 1; } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 22},
	{"return_mid_iteration", `function f(a: Cell[i32]): i32 { let i: i32 = 0; defer a.set(a.get() * 4 + 3); while (i < 5) { defer a.set(a.get() * 4 + 1); if (i == 2) { return 7; } i = i + 1; } return 0; }
function main(): i32 { let a: Cell[i32] = cell_new(0); if (f(a) != 7) { return 98; } return a.get(); }`, 87},
	{"errdefer_failing_iteration", `function f(a: Cell[i32], n: i32): Result[i32, i32] { let i: i32 = 0; while (i < n) { errdefer a.set(a.get() * 10 + 9); defer a.set(a.get() + 1); if (i == 2) { return Err(1); } i = i + 1; } return Ok(i); }
function main(): i32 { let a: Cell[i32] = cell_new(0); let b: Cell[i32] = cell_new(0); match (f(a, 2)) { Ok(v) => { if (v != 2) { return 97; } }, Err(e) => { return 96; } } if (a.get() != 2) { return 95; } match (f(b, 5)) { Ok(v) => { return 94; }, Err(e) => { if (e != 1) { return 93; } } } return b.get(); }`, 39},
	{"try_edge_mid_iteration", `function step(x: i32): Result[i32, i32] { if (x == 2) { return Err(9); } return Ok(x); }
function f(a: Cell[i32], n: i32): Result[i32, i32] { let i: i32 = 0; while (i < n) { defer a.set(a.get() + 1); let v: i32 = step(i)?; i = i + 1; } return Ok(i); }
function main(): i32 { let a: Cell[i32] = cell_new(0); match (f(a, 5)) { Ok(v) => { return 92; }, Err(e) => { if (e != 9) { return 91; } } } return a.get(); }`, 3},
	{"match_arm_in_loop", `function f(a: Cell[i32]): i32 { let i: i32 = 0; while (i < 3) { let o: Option[i32] = if (i == 1) { None } else { Some(i) }; match (o) { Some(v) => { defer a.set(a.get() * 10 + v + 1); }, None => { } } i = i + 1; } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 13},
	{"match_arm_in_loop_string", `function f(a: Cell[i32]): i32 { let i: i32 = 0; while (i < 3) { let o: Option[string] = if (i == 1) { None } else { Some("ab" + "c") }; match (o) { Some(v) => { defer a.set(a.get() * 10 + v.len()); }, None => { } } i = i + 1; } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 33},
	{"match_arms_two_escaping_bindings", `function f(a: Cell[i32]): i32 { let i: i32 = 0; while (i < 2) { let o: Option[string] = Some("ab" + "c"); let p: Option[i32] = if (i == 0) { Some(4) } else { None }; match (o) { Some(v) => { defer a.set(a.get() + v.len()); }, None => { } } match (p) { Some(w) => { defer a.set(a.get() + w * 10); }, None => { } } i = i + 1; } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 46},
	{"match_arm_in_loop_member", `struct A { name: string, x: i32 }
struct B { y: i32 }
type E = A | B;
function g(e: E): i32 { match (e) { A(p) => { return p.x + p.name.len(); }, B(q) => { return q.y; } } return 0; }
function f(a: Cell[i32]): i32 { let i: i32 = 0; while (i < 3) { let o: E = A { name: "ab" + "c", x: 2 }; if (i == 1) { o = B { y: 7 }; } match (o) { A(m) => { defer a.set(a.get() * 10 + g(m)); }, B(n) => { } } i = i + 1; } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 55},
	{"labelled_continue_ends_both_iterations", `function f(a: Cell[i32]): i32 { let i: i32 = 0; outer: while (i < 2) { defer a.set(a.get() * 3 + 2); i = i + 1; let j: i32 = 0; while (j < 3) { defer a.set(a.get() * 3 + 1); if (j == 0) { continue outer; } j = j + 1; } } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 50},
	{"lifo_within_one_iteration", `function f(a: Cell[i32]): i32 { let i: i32 = 0; while (i < 2) { defer a.set(a.get() * 3 + 1); defer a.set(a.get() * 3 + 2); i = i + 1; } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 70},
}

func TestSelfHostDeferInLoopIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range deferInLoopCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.main+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}

func TestSelfHostDeferInLoopIRCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	counted := map[string]bool{"match_arm_in_loop_string": true, "match_arm_in_loop_member": true, "match_arms_two_escaping_bindings": true}
	for _, tc := range deferInLoopCases {
		if !counted[tc.name] {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			stderr, code := cli.exitOf(t, tc.main+"\n", "x86-64-linux", "FERN_LEAKCHECK=1")
			if code != tc.want {
				t.Fatalf("exited %d, want %d\n%s", code, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
