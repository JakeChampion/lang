package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2649 — runtime helpers written in Fern instead of hand-written asm.
//
// Helpers migrated from hand-written asm strings to real Fern functions
// (asmcore.rt_src_*) are compiled through the normal function emitter, so they
// link as the ordinary user-function symbol `__fn_<name>` and are invoked via
// the stack-call convention, not as bare register-ABI `__fern_*` symbols.
//
// The behavioural cases in TestSelfHostAsmRunX86_64 already prove these helpers
// compute the right answers; they'd keep passing even if someone reverted to
// the hand-written asm. This test locks in the *migration* itself: for each
// migrated helper the emitted symbol must be the Fern-compiled `__fn_<name>`
// and the old hand-asm form (the bare label / its local loop label) must be
// gone. It shares the asm_run driver build with TestSelfHostAsmRunX86_64 (same
// sources → same driver-bin cache key), so it adds no driver rebuild.
func TestSelfHostRuntimeHelpersAreFern(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		sym  string   // the Fern-compiled symbol that must appear
		gone []string // hand-asm markers that must NOT appear
	}{
		{
			"i32_pow",
			"function main(): i32 { var n: i32 = 2; return n.pow(10); }",
			"__fn___fern_i32_pow",
			[]string{"\n__fern_i32_pow:", ".Lpow_loop"},
		},
		{
			"arr_i32_sum",
			"function main(): i32 { var xs: i32[] = [1, 2, 3]; return xs.sum(); }",
			"__fn___fern_arr_i32_sum",
			[]string{"\n__fern_arr_i32_sum:", ".Lai32_sum_loop"},
		},
		{
			"arr_i32_product",
			"function main(): i32 { var xs: i32[] = [1, 2, 3]; return xs.product(); }",
			"__fn___fern_arr_i32_product",
			[]string{"\n__fern_arr_i32_product:", ".Lai32_prod_loop"},
		},
		{
			"arr_i32_index_of",
			"function main(): i32 { var xs: i32[] = [5, 6, 7]; return xs.index_of(6); }",
			"__fn___fern_arr_i32_index_of",
			[]string{"\n__fern_arr_i32_index_of:", ".Lai32_idx_loop"},
		},
		{
			// AST path; the x86-64 IR path is covered by
			// TestSelfHostRuntimeHelperStrToI32IsFernIR.
			"str_to_i32",
			`function main(): i32 { return str_to_i32("42"); }`,
			"__fn___fern_str_to_i32",
			[]string{"\n__fern_str_to_i32:", ".Ls2i_loop"},
		},
		{
			// AST path; the x86-64 IR path is covered by
			// TestSelfHostRuntimeHelperStrToI32IsFernIR.
			"str_cmp",
			`function main(): i32 { if ("abc" < "abd") { return 1; } return 0; }`,
			"__fn___fern_str_cmp",
			[]string{"\n__fern_str_cmp:", ".Lstrcmp_loop"},
		},
		{
			// str_search bundle (starts_with/ends_with/index_of); one need emits
			// all three. AST path; IR covered by the IR lock-in test.
			"str_search",
			`function main(): i32 { if ("hello".starts_with("he")) { return 1; } return 0; }`,
			"__fn___fern_str_starts_with",
			[]string{"\n__fern_str_starts_with:", ".Lsw_loop", ".Lidx_outer"},
		},
		{
			// str_eq backs == on strings (and the map / arr_str helpers). AST
			// path; IR covered by the IR lock-in test.
			"str_eq",
			`function main(): i32 { if ("ab" == "ab") { return 1; } return 0; }`,
			"__fn___fern_str_eq",
			[]string{"\n__fern_str_eq:", ".Lstreq_loop"},
		},
		{
			// arr_str_index_of (string[].index_of/.contains) — AST-only, and the
			// first migrated helper that calls another (str_eq). Both Fern bodies
			// must be present + the hand-asm gone.
			"arr_str_index_of",
			`function main(): i32 { var xs: string[] = ["a", "b"]; return xs.index_of("b"); }`,
			"__fn___fern_arr_str_index_of",
			[]string{"\n__fern_arr_str_index_of:", ".Las_idx_loop"},
		},
		{
			"arr_i32_min",
			"function main(): i32 { var xs: i32[] = [5, 2, 7]; match (xs.min()) { Some(v) => { return v; }, None => { return 0; } } }",
			"__fn___fern_arr_i32_min",
			[]string{"\n__fern_arr_i32_min:", ".Lai32_min_loop"},
		},
		{
			"arr_i32_max",
			"function main(): i32 { var xs: i32[] = [5, 2, 7]; match (xs.max()) { Some(v) => { return v; }, None => { return 0; } } }",
			"__fn___fern_arr_i32_max",
			[]string{"\n__fern_arr_i32_max:", ".Lai32_max_loop"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver run: %v", err)
			}
			got := string(asm)
			// The migrated, Fern-compiled helper + its stack-call site.
			if !strings.Contains(got, tc.sym+":") {
				t.Errorf("emitted asm missing definition %q — %s no longer compiled from Fern?", tc.sym+":", tc.name)
			}
			if !strings.Contains(got, "call "+tc.sym) {
				t.Errorf("emitted asm missing %q — call site not migrated to the Fern symbol", "call "+tc.sym)
			}
			// The old hand-written asm form must be gone.
			for _, bad := range tc.gone {
				if strings.Contains(got, bad) {
					t.Errorf("emitted asm still contains hand-written form %q — %s migration regressed", bad, tc.name)
				}
			}
		})
	}
}
