package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcTupleBoxWasm proves the Phase-1e container-layout
// foundation for tuples: a tuple block is now rc-boxed via the generic
// $__fern_str_box (8-byte rc+bsz header, returns base+8), so it carries an
// rc word at [t-8] while every t-relative element access (t.N) is
// unchanged. Observed through __fern_rc_is_unique: a fresh tuple is unique
// (rc==1). Values + array/string elements (construction-inc'd) survive.
// Counting + recursive field-release ride on this foundation in later
// slices.
func TestSelfHostRcTupleBoxWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm tuple-box e2e")
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
		// A fresh tuple is rc-boxed at rc 1 => unique.
		{"tuple-fresh-unique", "function main(): i32 { var t = (5, 7); return __fern_rc_is_unique(t); }", 1},
		// Element values survive the rc header (t-relative access unchanged).
		{"tuple-values-intact", "function main(): i32 { var t = (30, 12); return t.0 + t.1; }", 42},
		// A tuple holding an array element: value intact, detector clean.
		{"tuple-holds-array", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var t = (xs, 9); return t.0[2] + t.1 + __rc_underflow_count(); }", 12},
		// Destructuring a tuple reads both elements correctly.
		{"tuple-destructure", "function main(): i32 { var t = (8, 34); var (a, b) = t; return a + b; }", 42},
		// Counting milestone (free off): an owned tuple local is released
		// (rc dec) at exit, value-correct + detector clean.
		{"tuple-swept-clean", "function main(): i32 { var t = (5, 7); return t.0 + t.1 + __rc_underflow_count(); }", 12},
		// Aliasing a tuple: the alias is inc'd, both swept, balanced.
		{"tuple-alias-clean", "function main(): i32 { var t = (3, 4); var u = t; return u.0 + t.1 + __rc_underflow_count(); }", 7},
		// A tuple re-bound each loop iteration: detector stays clean.
		{"tuple-loop-clean", "function main(): i32 { var s = 0; var k = 0; while (k < 1000) { var t = (k, 2); s = s + t.1; k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
		// Construction-store inc: storing a tuple into a container retains it
		// (source no longer unique), values intact, detector clean — the prep
		// that lets a tuple stored in a container survive once free flips on.
		{"tuple-in-array-retained", "function main(): i32 { var t = (3, 4); var arr = [t]; var u = __fern_rc_is_unique(t); return u + arr[0].0 + __rc_underflow_count(); }", 3},
		{"tuple-in-tuple-retained", "function main(): i32 { var t = (5, 6); var o = (t, 99); var u = __fern_rc_is_unique(t); return u + o.0.1 + __rc_underflow_count(); }", 6},
		// FREE + recursive field-release: freeing a tuple at exit releases its
		// rc-tracked array element (the source xs is dec'd to 0 by the tuple's
		// recursive release) — value-correct + detector clean.
		{"tuple-array-elem-released", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var t = (xs, 9); return t.0[2] + t.1 + __rc_underflow_count(); }", 12},
		// Same for a string element.
		{"tuple-string-elem-released", "function main(): i32 { var s: string = \"ab\" + \"cd\"; var t = (s, 5); return t.0.len() + t.1 + __rc_underflow_count(); }", 9},
		// A build-tuple-with-array-element churn (bare-ident array element →
		// recursively released each time the tuple is freed): detector clean
		// with free on across many cycles.
		{"tuple-array-churn-clean", "function mk(): i32 { var a: i32[] = [1, 2, 3, 4, 5, 6, 7, 8]; var t = (a, 5); return t.0[7] + t.1; } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 6},
		// DEPTH: a tuple holding a string[] element ('A' kind) now deep-releases
		// the array's string elements via arr_dec_ptr, not just the buffer —
		// value-correct + detector clean.
		{"tuple-strarray-elem-released", "function main(): i32 { var strs: string[] = [\"a\" + \"b\", \"c\" + \"d\"]; var t = (strs, 5); return t.0[0].len() + t.1 + __rc_underflow_count(); }", 7},
		// SAFETY: a tuple holding an i32[] element (values >= heap_base and even,
		// which look like heap pointers) must stay FLAT ('a' kind) — the scalar
		// elements must NOT be arr_dec'd as pointers (that would corrupt).
		{"tuple-i32array-flat-safe", "function main(): i32 { var ns: i32[] = [262184, 262192, 262200]; var t = (ns, 4); return t.0.len() + t.1 + __rc_underflow_count(); }", 7},
		// A churn of tuples each holding a fresh string[]: deep element release
		// reclaims the strings (no growth), detector clean across many cycles.
		{"tuple-strarray-churn-clean", "function mk(): i32 { var t = ([\"x\" + \"y\", \"z\" + \"w\"], 3); return t.0[0].len() + t.1; } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
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
