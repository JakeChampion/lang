package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcStrBoxWasm proves the Phase-1e string rc-box layout
// foundation: HEAP strings (concat / to_upper / to_lower / repeat / substr
// / join / string_from_bytes_unchecked / i32_to_str) are now allocated through
// $__fern_str_box, so they carry an rc word at [s-8] (rc 1 for a fresh
// owner) while static string LITERALS stay in the data section, unboxed —
// the rc helpers' address guard treats them as immortal. String VALUES are
// unchanged (every access is s-relative), and counting and release keep the
// over-release detector clean.
func TestSelfHostRcStrBoxWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm str-box e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// String values survive the new layout: len + bytes read correctly.
		{"concat-value-intact", "function main(): i32 { let s: string = \"foo\" + \"barbaz\"; return s.len(); }", 9},
		{"substr-value-intact", "function main(): i32 { let s: string = \"hello world\"; let t: string = slice_unchecked(s, 0, 5) + \"\"; return t.len(); }", 5},
		// String counting milestone (free OFF): an owned concat local is
		// released (rc dec) at exit, value-correct + over-release detector 0.
		{"concat-swept-clean", "function main(): i32 { let a: string = \"x\"; let b: string = \"yz\"; let s: string = a + b; return s.len() + __rc_underflow_count(); }", 3},
		// Aliasing a heap concat: the alias is inc'd, both swept, balanced.
		{"concat-alias-clean", "function main(): i32 { let s: string = \"ab\" + \"cd\"; let t: string = s; return t.len() + __rc_underflow_count(); }", 4},
		// A concat-in-a-loop (reassign): intermediates leak (sound, free off)
		// but the detector stays clean and the value is correct.
		{"concat-loop-clean", "function main(): i32 { let s: string = \"\"; let i = 0; while (i < 5) { s = s + \"x\"; i = i + 1; } return s.len() + __rc_underflow_count(); }", 5},
		// String FREE on: a built string returned (move-on-return) survives
		// in the caller — not freed under it. Detector clean.
		{"string-return-survives", "function build(): string { let s: string = \"ab\" + \"cd\"; return s; } function main(): i32 { let x: string = build(); let y: string = build(); return x.len() + y.len() + __rc_underflow_count(); }", 8},
		// A built string stored in a struct survives the builder's exit sweep
		// (construction-inc keeps it at rc 1 for the struct). Detector clean.
		{"string-struct-survives-free", "struct H { name: string } function mk(): H { let s: string = \"xy\" + \"z\"; return H { name: s }; } function main(): i32 { let h = mk(); return h.name.len() + __rc_underflow_count(); }", 3},
		// A locally-built string used then dropped is freed at exit; a churn
		// stays value-correct + detector clean (no over-release with free on).
		{"string-build-use-churn", "function main(): i32 { let n = 0; let k = 0; while (k < 2000) { let s: string = \"ab\" + \"cd\"; n = n + s.len(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 6},
		// Reassign reclaim: `s = s + x` in a loop now releases each prior
		// string (StmtAssign cow-guarded dec-on-overwrite) instead of leaking
		// all but the last — value-correct + detector clean.
		{"string-builder-loop-reclaim", "function main(): i32 { let s: string = \"\"; let i = 0; while (i < 100000) { s = s + \"x\"; i = i + 1; } return (s.len() % 7) + __rc_underflow_count(); }", 5},
		// Rebind reclaim: `let s = …` re-bound each iteration is released
		// per-iteration (StmtVar dec-on-overwrite). detector clean.
		{"string-rebind-loop-reclaim", "function main(): i32 { let n = 0; let k = 0; while (k < 100000) { let s: string = \"ab\" + \"cd\"; n = n + s.len(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 6},
		// Method / call / slice string results are now counted+swept too.
		{"string-method-result-swept", "function main(): i32 { let s: string = \"AbC\".to_ascii_upper(); return s.len() + __rc_underflow_count(); }", 3},
		{"string-fn-result-swept", "function build(): string { return \"x\" + \"yz\"; } function main(): i32 { let s: string = build(); return s.len() + __rc_underflow_count(); }", 3},
		{"string-slice-result-swept", "function main(): i32 { let src: string = \"abcdef\"; let s: string = slice_unchecked(src, 1, 4) + \"\"; return s.len() + __rc_underflow_count(); }", 3},
		// Regression: a function returning a BORROWED string field with NO
		// swept locals must still return-retain it, or the caller's sweep of
		// the result frees the field still in use (the node_head/watbin UAF).
		{"string-borrowed-field-return", "struct H { name: string } function getname(h: H): string { return h.name; } function main(): i32 { let s: string = \"ab\" + \"cd\"; let h = H { name: s }; let n: string = getname(h); return n.len() + h.name.len() + __rc_underflow_count(); }", 8},
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
