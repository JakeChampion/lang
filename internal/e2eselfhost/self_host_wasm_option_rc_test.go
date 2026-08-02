package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcOptionBoxWasm proves the Phase-1f Option/Result rc-box layout
// foundation: a variant box ([tag@0][payload@4]) is now rc-headered via the
// generic $__fern_str_box (8-byte rc+bsz header, returns base+8), so it
// carries an rc word at [p-8] while tag@[p] and payload@[p+4] (every
// p-relative access — match dispatch, `?` unwrap) are unchanged. Observed
// through __fern_rc_is_unique: a fresh Some/Ok box is unique (rc==1). Counting
// + the payload recursive release ride on this foundation in later slices.
// (The io/extern option builders — read_file etc. — stay raw for now;
// layout-only never sweeps options, so the mix is value-safe.)
func TestSelfHostRcOptionBoxWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm option-box e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// A fresh Some box is rc-boxed at rc 1 => unique.
		{"some-fresh-unique", "function main(): i32 { var o = Some(42); return __fern_rc_is_unique(o); }", 1},
		// Payload value survives the rc header (match reads tag@[p], payload@[p+4]).
		{"some-match-intact", "function main(): i32 { var o = Some(30); match (o) { Some(x) => { return x + 12; }, None => { return 0; } } }", 42},
		// None (no payload) dispatches correctly under the rc header.
		{"none-match-intact", "function get(b: i32): Option[i32] { if (b > 0) { return Some(5); } return None; } function main(): i32 { match (get(0)) { Some(x) => { return x; }, None => { return 41; } } }", 41},
		// Result Ok / Err both rc-boxed; value intact + detector clean.
		{"result-ok-intact", "function getr(b: i32): Result[i32, i32] { if (b > 0) { return Ok(7); } return Err(9); } function main(): i32 { match (getr(1)) { Ok(x) => { return x + __fern_rc_underflow_count(); }, Err(e) => { return 0; } } }", 7},
		{"result-err-intact", "function getr(b: i32): Result[i32, i32] { if (b > 0) { return Ok(7); } return Err(35); } function main(): i32 { match (getr(0)) { Ok(x) => { return x; }, Err(e) => { return e + 7; } } }", 42},
		// `?` unwrap reads the boxed Some payload / propagates the None box.
		{"question-unwrap-intact", "function inner(): Option[i32] { return Some(40); } function outer(): Option[i32] { var x = inner()?; return Some(x + 2); } function main(): i32 { match (outer()) { Some(v) => { return v; }, None => { return 0; } } }", 42},
		// A Some holding a heap string: payload intact through the boxed layout
		// (the payload release rides on later slices; here just value + detector).
		{"some-string-payload-intact", "function main(): i32 { var s: string = \"ab\" + \"cd\"; var o = Some(s); match (o) { Some(v) => { return v.len() + __fern_rc_underflow_count(); }, None => { return 0; } } }", 4},
		// COUNTING milestone (free off): an owned option local is released (rc
		// dec) at exit, value-correct + detector clean.
		{"option-swept-clean", "function main(): i32 { var o = Some(40); match (o) { Some(x) => { return x + 2 + __fern_rc_underflow_count(); }, None => { return 0; } } }", 42},
		// Aliasing an option: the alias is inc'd, both swept, balanced.
		{"option-alias-clean", "function main(): i32 { var o = Some(20); var u = o; match (u) { Some(x) => { return x + 22 + __fern_rc_underflow_count(); }, None => { return 0; } } }", 42},
		// A Some holding a heap string, swept (counting) — detector clean.
		{"option-string-payload-swept", "function main(): i32 { var s: string = \"ab\" + \"cd\"; var o = Some(s); match (o) { Some(v) => { return v.len() + 38 + __fern_rc_underflow_count(); }, None => { return 0; } } }", 42},
		// Move-on-return: a builder hands its option to the caller (excluded
		// from the builder's sweep), the caller sweeps it — balanced, clean.
		{"option-move-return-clean", "function mk(b: i32): Option[i32] { if (b > 0) { return Some(33); } return None; } function main(): i32 { var o = mk(1); var p = mk(0); match (o) { Some(x) => { return x + 9 + __fern_rc_underflow_count(); }, None => { return 0; } } }", 42},
		// An option re-bound each loop iteration: detector stays clean (counting;
		// free off, so intermediates leak soundly).
		{"option-loop-clean", "function main(): i32 { var s = 0; var k = 0; while (k < 1000) { var o = Some(k); match (o) { Some(x) => { s = s + 2; }, None => {} } k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 5},
		// read_file Result: an owned option from a builtin is swept (counting) —
		// the missing-file Err path is value-correct + detector clean.
		{"option-readfile-result-swept", "function main(): i32 { var r = read_file(\"definitely_missing_xyz.txt\"); match (r) { Ok(s) => { return s.len(); }, Err(e) => { return 42 + __fern_rc_underflow_count(); } } }", 42},
		// FREE + tag-guarded payload release: freeing a Some(heap string) at exit
		// releases the string payload — value-correct + detector clean.
		{"option-string-payload-released", "function main(): i32 { var s: string = \"ab\" + \"cd\"; var o = Some(s); match (o) { Some(v) => { return v.len() + 38 + __fern_rc_underflow_count(); }, None => { return 0; } } }", 42},
		// SAFETY: an Option<i32> scalar payload (a value >= heap_base, even, that
		// looks like a heap pointer) must NOT be released — struct_field_kind_char
		// returns 'i' for i32, so the payload is skipped (no corruption).
		{"option-i32-payload-flat-safe", "function main(): i32 { var o = Some(262184); match (o) { Some(x) => { return 42 + __fern_rc_underflow_count(); }, None => { return 0; } } }", 42},
		// Builder-escape: mk returns Some(string) (move-on-return); the caller's
		// payload release frees the string exactly once (the enum_box_retain
		// payload retain + the move balance it).
		{"option-builder-escape-clean", "function mk(): Option[string] { var s: string = \"x\" + \"yz\"; return Some(s); } function main(): i32 { var o = mk(); var p = mk(); match (o) { Some(v) => { return v.len() + 39 + __fern_rc_underflow_count(); }, None => { return 0; } } }", 42},
		// A churn of Some(heap string): payload release reclaims the strings (no
		// growth), detector clean across many cycles.
		{"option-string-churn-clean", "function mk(): i32 { var o = Some(\"a\" + \"b\"); match (o) { Some(v) => { return v.len(); }, None => { return 0; } } } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 2},
		// RECLAIM: an option local re-bound (`var o = Some(…)`) each loop
		// iteration now releases the prior value (payload-aware, cow-guarded)
		// instead of leaking it — 100k iters stay reclaimed + detector clean.
		{"option-rebind-loop-reclaim", "function main(): i32 { var n = 0; var k = 0; while (k < 100000) { var o = Some(\"a\" + \"b\"); match (o) { Some(v) => { n = n + v.len(); }, None => {} } k = k + 1; } return (n % 7) + __fern_rc_underflow_count(); }", 3},
		// RECLAIM: a reassigned option (`o = Some(…)`) each iteration reclaims the
		// old (box + its string payload) across 100k cycles, detector clean.
		{"option-reassign-loop-reclaim", "function main(): i32 { var o = Some(\"xx\" + \"yy\"); var k = 0; while (k < 100000) { o = Some(\"z\" + \"w\"); k = k + 1; } match (o) { Some(v) => { return v.len() + __fern_rc_underflow_count(); }, None => { return 0; } } }", 2},
		// Result Err-payload release: freeing a Result whose Err type is a heap
		// string releases the Err string (tag-1 path in emit_option_release,
		// driven by ol_err_payloads). Value-correct + detector clean.
		{"result-err-string-released", "function fail(): Result[i32, string] { return Err(\"e\" + \"rr\"); } function main(): i32 { var r: Result[i32, string] = fail(); match (r) { Ok(x) => { return x; }, Err(e) => { return e.len() + 39 + __fern_rc_underflow_count(); } } }", 42},
		// The Ok side still releases (Ok string payload), Err type scalar.
		{"result-ok-string-released", "function good(): Result[string, i32] { return Ok(\"o\" + \"k!\"); } function main(): i32 { var r: Result[string, i32] = good(); match (r) { Ok(s) => { return s.len() + 39 + __fern_rc_underflow_count(); }, Err(e) => { return e; } } }", 42},
		// A churn building + freeing an Err(heap-string) 50k times: the Err
		// string is reclaimed each cycle (no growth), detector clean.
		{"result-err-string-churn-clean", "function fail(): Result[i32, string] { return Err(\"a\" + \"b\"); } function mk(): i32 { var r: Result[i32, string] = fail(); match (r) { Ok(x) => { return x; }, Err(e) => { return e.len(); } } } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }", 2},
		// RC-PAYLOAD (scalar-array) Option consume-by-match free on wasm: a fresh
		// `var o = Some([..])` consumed by one match now DEEP-DROPS its array payload
		// (op_opt_payload → dec) then frees the box, right after the match — the same
		// shared irlower classifier (consumed_rcpayload_option_frees) that drives the
		// register backends. Value intact + detector clean (payload + box each freed
		// exactly once; the borrow `v[i]` ends before the post-match free).
		{"option-arr-payload-freed", `function main(): i32 { var o: Option[i32[]] = Some([10, 20, 30]); var r = 0; match (o) { Some(v) => { r = v[0] + v[2]; }, None => {} } return r + __fern_rc_underflow_count(); }`, 40},
		// Un-annotated Some([..]) (Option infers the single type param) fires too.
		{"option-arr-unannotated-freed", `function main(): i32 { var o = Some([1, 2, 3]); var r = 0; match (o) { Some(v) => { r = v[0] + v[1] + v[2] + 36; }, None => {} } return r + __fern_rc_underflow_count(); }`, 42},
		// Churn building + consuming Some([..]) 50k times: the array payload is
		// reclaimed each cycle (no growth), detector clean across all cycles.
		{"option-arr-payload-churn-clean", `function mk(): i32 { var o: Option[i32[]] = Some([1, 2, 3]); var r = 0; match (o) { Some(v) => { r = v.len(); }, None => {} } return r; } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __fern_rc_underflow_count(); }`, 3},
		// Result Ok([..]) array payload freed the same way on wasm (tag 0).
		{"result-ok-arr-payload-freed", `function main(): i32 { var r: Result[i32[], i32] = Ok([5, 6, 7]); var x = 0; match (r) { Ok(v) => { x = v[0] + v[2] + 30; }, Err(e) => { x = e; } } return x + __fern_rc_underflow_count(); }`, 42},
		// Scalar-only STRUCT payload Option freed on wasm: a fresh `Some(P{..})` frees
		// the payload box (shallow — inline scalars) then the option box, right after
		// its consuming match. Value intact + detector clean.
		{"option-struct-payload-freed", `struct P { x: i32, y: i32 } function main(): i32 { var o: Option[P] = Some(P { x: 18, y: 24 }); var r = 0; match (o) { Some(p) => { r = p.x + p.y; }, None => {} } return r + __fern_rc_underflow_count(); }`, 42},
		// Array-FIELD struct payload on wasm: `Some(Buf{xs:[..],n})` shallow-frees the
		// Buf box + option box; the array field is machinery-owned (no deep-drop). Value
		// + detector clean.
		{"option-struct-arrfield-payload-freed", `struct Buf { xs: i32[], n: i32 } function main(): i32 { var o: Option[Buf] = Some(Buf { xs: [10, 20, 30], n: 9 }); var r = 0; match (o) { Some(b) => { r = b.xs[1] + b.n; }, None => {} } return r + 13 + __fern_rc_underflow_count(); }`, 42},
		// PRECISE drop of a scalar Option last-used in a NESTED if-block (no top-level
		// match) — precise_drop_names frees the rc box right after the if. Value +
		// detector clean (the box is freed exactly once, on the shared classifier that
		// drives wasm too).
		{"option-precise-if-freed", `function f(n: i32): i32 { var o: Option[i32] = Some(40); var c = 0; if (n > 0) { match (o) { Some(v) => { c = v; }, None => {} } } return c + 2 + __fern_rc_underflow_count(); } function main(): i32 { return f(5); }`, 42},
		// PRECISE drop of an rc-PAYLOAD (array) Option last-used in a NESTED if-block —
		// emit_opt_payload_drop frees the payload + box right after the if. Value +
		// detector clean.
		{"option-arr-precise-if-freed", `function f(n: i32): i32 { var o: Option[i32[]] = Some([10, 20, 30]); var c = 0; if (n > 0) { match (o) { Some(v) => { c = v[0] + v[2]; }, None => {} } } return c + 2 + __fern_rc_underflow_count(); } function main(): i32 { return f(5); }`, 42},
		// PRECISE drop of a scalar RESULT in a nested if (kind-gated, so the Result box
		// — not just an Option — is freed). Value + detector clean.
		{"result-scalar-precise-if-freed", `function f(n: i32): i32 { var r: Result[i32, i32] = Ok(40); var c = 0; if (n > 0) { match (r) { Ok(v) => { c = v; }, Err(e) => { c = e; } } } return c + 2 + __fern_rc_underflow_count(); } function main(): i32 { return f(5); }`, 42},
		// PRECISE drop of an rc-PAYLOAD (array) RESULT Ok box in a nested if.
		{"result-arr-precise-if-freed", `function f(n: i32): i32 { var r: Result[i32[], i32] = Ok([10, 20, 30]); var c = 0; if (n > 0) { match (r) { Ok(v) => { c = v[0] + v[2]; }, Err(e) => { c = e; } } } return c + 2 + __fern_rc_underflow_count(); } function main(): i32 { return f(5); }`, 42},
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
