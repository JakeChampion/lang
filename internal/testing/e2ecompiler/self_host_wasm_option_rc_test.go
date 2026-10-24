package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostRcOptionBoxWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm option-box e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"some-match-intact", "function main(): i32 { let o = Some(30); match (o) { Some(x) => { return x + 12; }, None => { return 0; } } }", 42},
		{"none-match-intact", "function get(b: i32): Option[i32] { if (b > 0) { return Some(5); } return None; } function main(): i32 { match (get(0)) { Some(x) => { return x; }, None => { return 41; } } }", 41},
		{"result-ok-intact", "function getr(b: i32): Result[i32, i32] { if (b > 0) { return Ok(7); } return Err(9); } function main(): i32 { match (getr(1)) { Ok(x) => { return x + __rc_underflow_count(); }, Err(e) => { return 0; } } }", 7},
		{"result-err-intact", "function getr(b: i32): Result[i32, i32] { if (b > 0) { return Ok(7); } return Err(35); } function main(): i32 { match (getr(0)) { Ok(x) => { return x; }, Err(e) => { return e + 7; } } }", 42},
		{"question-unwrap-intact", "function inner(): Option[i32] { return Some(40); } function outer(): Option[i32] { let x = inner()?; return Some(x + 2); } function main(): i32 { match (outer()) { Some(v) => { return v; }, None => { return 0; } } }", 42},
		{"some-string-payload-intact", "function main(): i32 { let s: string = \"ab\" + \"cd\"; let o = Some(s); match (o) { Some(v) => { return v.len() + __rc_underflow_count(); }, None => { return 0; } } }", 4},
		{"option-swept-clean", "function main(): i32 { let o = Some(40); match (o) { Some(x) => { return x + 2 + __rc_underflow_count(); }, None => { return 0; } } }", 42},
		{"option-alias-clean", "function main(): i32 { let o = Some(20); let u = o; match (u) { Some(x) => { return x + 22 + __rc_underflow_count(); }, None => { return 0; } } }", 42},
		{"option-string-payload-swept", "function main(): i32 { let s: string = \"ab\" + \"cd\"; let o = Some(s); match (o) { Some(v) => { return v.len() + 38 + __rc_underflow_count(); }, None => { return 0; } } }", 42},
		{"option-move-return-clean", "function mk(b: i32): Option[i32] { if (b > 0) { return Some(33); } return None; } function main(): i32 { let o = mk(1); let p = mk(0); match (o) { Some(x) => { return x + 9 + __rc_underflow_count(); }, None => { return 0; } } }", 42},
		{"option-loop-clean", "function main(): i32 { let s = 0; let k = 0; while (k < 1000) { let o = Some(k); match (o) { Some(x) => { s = s + 2; }, None => {} } k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
		{"option-readfile-result-swept", "function main(): i32 { let r = read_file(\"definitely_missing_xyz.txt\"); match (r) { Ok(s) => { return s.len(); }, Err(e) => { return 42 + __rc_underflow_count(); } } }", 42},
		{"option-string-payload-released", "function main(): i32 { let s: string = \"ab\" + \"cd\"; let o = Some(s); match (o) { Some(v) => { return v.len() + 38 + __rc_underflow_count(); }, None => { return 0; } } }", 42},
		{"option-i32-payload-flat-safe", "function main(): i32 { let o = Some(262184); match (o) { Some(x) => { return 42 + __rc_underflow_count(); }, None => { return 0; } } }", 42},
		{"option-builder-escape-clean", "function mk(): Option[string] { let s: string = \"x\" + \"yz\"; return Some(s); } function main(): i32 { let o = mk(); let p = mk(); match (o) { Some(v) => { return v.len() + 39 + __rc_underflow_count(); }, None => { return 0; } } }", 42},
		{"option-string-churn-clean", "function mk(): i32 { let o = Some(\"a\" + \"b\"); match (o) { Some(v) => { return v.len(); }, None => { return 0; } } } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 2},
		{"option-rebind-loop-reclaim", "function main(): i32 { let n = 0; let k = 0; while (k < 100000) { let o = Some(\"a\" + \"b\"); match (o) { Some(v) => { n = n + v.len(); }, None => {} } k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 3},
		{"option-reassign-loop-reclaim", "function main(): i32 { let o = Some(\"xx\" + \"yy\"); let k = 0; while (k < 100000) { o = Some(\"z\" + \"w\"); k = k + 1; } match (o) { Some(v) => { return v.len() + __rc_underflow_count(); }, None => { return 0; } } }", 2},
		{"result-err-string-released", "function fail(): Result[i32, string] { return Err(\"e\" + \"rr\"); } function main(): i32 { let r: Result[i32, string] = fail(); match (r) { Ok(x) => { return x; }, Err(e) => { return e.len() + 39 + __rc_underflow_count(); } } }", 42},
		{"result-ok-string-released", "function good(): Result[string, i32] { return Ok(\"o\" + \"k!\"); } function main(): i32 { let r: Result[string, i32] = good(); match (r) { Ok(s) => { return s.len() + 39 + __rc_underflow_count(); }, Err(e) => { return e; } } }", 42},
		{"result-err-string-churn-clean", "function fail(): Result[i32, string] { return Err(\"a\" + \"b\"); } function mk(): i32 { let r: Result[i32, string] = fail(); match (r) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 2},
		{"option-arr-payload-freed", `function main(): i32 { let o: Option[i32[]] = Some([10, 20, 30]); let r = 0; match (o) { Some(v) => { r = v[0] + v[2]; }, None => {} } return r + __rc_underflow_count(); }`, 40},
		{"option-arr-unannotated-freed", `function main(): i32 { let o = Some([1, 2, 3]); let r = 0; match (o) { Some(v) => { r = v[0] + v[1] + v[2] + 36; }, None => {} } return r + __rc_underflow_count(); }`, 42},
		{"option-arr-payload-churn-clean", `function mk(): i32 { let o: Option[i32[]] = Some([1, 2, 3]); let r = 0; match (o) { Some(v) => { r = v.len(); }, None => {} } return r; } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }`, 3},
		{"result-ok-arr-payload-freed", `function main(): i32 { let r: Result[i32[], i32] = Ok([5, 6, 7]); let x = 0; match (r) { Ok(v) => { x = v[0] + v[2] + 30; }, Err(e) => { x = e; } } return x + __rc_underflow_count(); }`, 42},
		{"option-struct-payload-freed", `struct P { x: i32, y: i32 } function main(): i32 { let o: Option[P] = Some(P { x: 18, y: 24 }); let r = 0; match (o) { Some(p) => { r = p.x + p.y; }, None => {} } return r + __rc_underflow_count(); }`, 42},
		{"option-struct-arrfield-payload-freed", `struct Buf { xs: i32[], n: i32 } function main(): i32 { let o: Option[Buf] = Some(Buf { xs: [10, 20, 30], n: 9 }); let r = 0; match (o) { Some(b) => { r = b.xs[1] + b.n; }, None => {} } return r + 13 + __rc_underflow_count(); }`, 42},
		{"option-precise-if-freed", `function f(n: i32): i32 { let o: Option[i32] = Some(40); let c = 0; if (n > 0) { match (o) { Some(v) => { c = v; }, None => {} } } return c + 2 + __rc_underflow_count(); } function main(): i32 { return f(5); }`, 42},
		{"option-arr-precise-if-freed", `function f(n: i32): i32 { let o: Option[i32[]] = Some([10, 20, 30]); let c = 0; if (n > 0) { match (o) { Some(v) => { c = v[0] + v[2]; }, None => {} } } return c + 2 + __rc_underflow_count(); } function main(): i32 { return f(5); }`, 42},
		{"result-scalar-precise-if-freed", `function f(n: i32): i32 { let r: Result[i32, i32] = Ok(40); let c = 0; if (n > 0) { match (r) { Ok(v) => { c = v; }, Err(e) => { c = e; } } } return c + 2 + __rc_underflow_count(); } function main(): i32 { return f(5); }`, 42},
		{"result-arr-precise-if-freed", `function f(n: i32): i32 { let r: Result[i32[], i32] = Ok([10, 20, 30]); let c = 0; if (n > 0) { match (r) { Ok(v) => { c = v[0] + v[2]; }, Err(e) => { c = e; } } } return c + 2 + __rc_underflow_count(); } function main(): i32 { return f(5); }`, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
