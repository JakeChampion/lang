package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcStrBoxWasm proves the Phase-1e string rc-box layout
// foundation: HEAP strings (concat / to_upper / to_lower / repeat / substr
// / join / string_from_bytes / i32_to_str) are now allocated through
// $__fern_str_box, so they carry an rc word at [s-8] (rc 1 for a fresh
// owner) while static string LITERALS stay in the data section, unboxed —
// the rc helpers' address guard treats them as immortal. Observed through
// __fern_rc_is_unique: a fresh heap string is unique (rc==1 => 1); a
// literal is not (guarded => 0). String VALUES are unchanged (every access
// is s-relative); counting + release ride on this foundation in later
// slices. (str_box reuses $__fern_arr_dec / the size-class freelist for
// release, since a string is flat with no rc-tracked children.)
func TestSelfHostRcStrBoxWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm str-box e2e")
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
		// A fresh heap string (concatenation) is rc-boxed at rc 1 => unique.
		{"concat-fresh-unique", "function main(): i32 { var s: string = \"ab\" + \"cd\"; return __fern_rc_is_unique(s); }", 1},
		// to_upper result is a fresh heap string too.
		{"upper-fresh-unique", "function main(): i32 { var s: string = \"abc\".to_upper(); return __fern_rc_is_unique(s); }", 1},
		// A static string literal is NOT boxed (data section, below
		// heap_base) — the address guard reports it as immortal / not unique.
		{"literal-not-unique", "function main(): i32 { var s: string = \"hello\"; return __fern_rc_is_unique(s); }", 0},
		// String values survive the new layout: len + bytes read correctly.
		{"concat-value-intact", "function main(): i32 { var s: string = \"foo\" + \"barbaz\"; return s.len(); }", 9},
		{"substr-value-intact", "function main(): i32 { var s: string = \"hello world\"; var t: string = s[0:5]; return t.len(); }", 5},
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
