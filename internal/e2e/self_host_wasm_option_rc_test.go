package e2e

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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "wasm.fern", "wasm_run.fern"} {
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
