package e2eselfhost

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
			"i32_gcd",
			"function main(): i32 { var n: i32 = 12; return n.gcd(18); }",
			"__fn___fern_i32_gcd",
			[]string{"\n__fern_i32_gcd:", ".Lgcd_loop"},
		},
		{
			// i32_lcm — pure-scalar helper that calls another (gcd) via `.gcd()`,
			// the scalar analogue of arr_str_index_of→str_eq. Both Fern bodies
			// must be present and the hand-asm gone.
			"i32_lcm",
			"function main(): i32 { var n: i32 = 4; return n.lcm(6); }",
			"__fn___fern_i32_lcm",
			[]string{"\n__fern_i32_lcm:", ".Llcm_zero"},
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
			// str_trim (s.trim()) — AST-only; a zero-copy slice helper, un-bundled
			// from the str_search need. The IR path keeps its own str_trim emission.
			"str_trim",
			`function main(): i32 { return "  hi ".trim().len(); }`,
			"__fn___fern_str_trim",
			[]string{"\n__fern_str_trim:", ".Ltrim_front"},
		},
		{
			// str_lines (s.lines()) — was an INLINE lowering on the AST path; now a
			// Fern helper composing str_split + an array slice. The old inline
			// labels (.Llines_have_) must be gone.
			"str_lines",
			`function main(): i32 { return "a\nb\n".lines().len(); }`,
			"__fn___fern_str_lines",
			[]string{".Llines_have_"},
		},
		{
			// str_bytes (s.bytes() / s.as_bytes()) — a Fern helper that appends each
			// byte (arr_push). The old hand-asm (__fern_str_bytes: / .Lbytes_loop) gone.
			"str_bytes",
			`function main(): i32 { return "abc".bytes().len(); }`,
			"__fn___fern_str_bytes",
			[]string{"\n__fern_str_bytes:", ".Lbytes_loop"},
		},
		{
			// str_chars (s.chars()) — a Fern helper that appends each 1-char slice
			// (arr_push). The old hand-asm (__fern_str_chars: / .Lchars_loop) gone.
			"str_chars",
			`function main(): i32 { return "abc".chars().len(); }`,
			"__fn___fern_str_chars",
			[]string{"\n__fern_str_chars:", ".Lchars_loop"},
		},
		{
			// chr — first Tier-2 helper via the raw-memory intrinsics (#2649).
			// The old register-ABI hand-asm (a bare __fern_chr: label) is gone;
			// only the Fern-compiled __fn___fern_chr remains.
			"chr",
			`function main(): i32 { return chr(65)[0]; }`,
			"__fn___fern_chr",
			[]string{"\n__fern_chr:"},
		},
		{
			// str_concat — backs `+` on strings, Tier-2 via the intrinsics (#2649).
			// The old register-ABI hand-asm (__fern_str_concat: / .Lstrconcat_a_loop)
			// is gone; the `+` call site now targets __fn___fern_str_concat.
			"str_concat",
			`function main(): i32 { var s: string = "ab" + "cd"; return s.len(); }`,
			"__fn___fern_str_concat",
			[]string{"\n__fern_str_concat:", ".Lstrconcat_a_loop"},
		},
		{
			// i32_to_string — backs (n).to_string() / the free fn, Tier-2 via the
			// intrinsics (#2649). The old register-ABI hand-asm (the bare
			// __fern_i32_to_string: label + .Li2s_div loop) is gone.
			"i32_to_string",
			`function main(): i32 { return i32_to_string(42).len(); }`,
			"__fn___fern_i32_to_string",
			[]string{"\n__fern_i32_to_string:", ".Li2s_div"},
		},
		{
			// str_to_upper — Tier-2 via the intrinsics (#2649), un-bundled from
			// str_search. The old register-ABI hand-asm (__fern_str_to_upper: /
			// .Lupper_loop) is gone.
			"str_to_upper",
			`function main(): i32 { return "aB".to_ascii_upper()[0]; }`,
			"__fn___fern_str_to_upper",
			[]string{"\n__fern_str_to_upper:", ".Lupper_loop"},
		},
		{
			// str_to_lower — the lower-case sibling, same str_case-gated migration.
			"str_to_lower",
			`function main(): i32 { return "Ab".to_ascii_lower()[0]; }`,
			"__fn___fern_str_to_lower",
			[]string{"\n__fern_str_to_lower:", ".Llower_loop"},
		},
		{
			// str_repeat (s.repeat(n) / str_repeat(s, n)) — Tier-2 via the
			// raw-memory intrinsics (#2649). The old register-ABI hand-asm (a bare
			// __fern_str_repeat: label + .Lrep_outer loop) is gone; the call site
			// targets __fn___fern_str_repeat via the stack ABI.
			"str_repeat",
			`function main(): i32 { return "ab".repeat(3).len(); }`,
			"__fn___fern_str_repeat",
			[]string{"\n__fern_str_repeat:", ".Lrep_outer"},
		},
		{
			// str_reverse (s.reverse()) — Tier-2 via the raw-memory intrinsics
			// (#2649). The old register-ABI hand-asm (a bare __fern_str_reverse:
			// label + .Lstr_rev_loop) is gone; the call site targets
			// __fn___fern_str_reverse via the stack ABI.
			"str_reverse",
			`function main(): i32 { return "abc".reverse()[0]; }`,
			"__fn___fern_str_reverse",
			[]string{"\n__fern_str_reverse:", ".Lstr_rev_loop"},
		},
		{
			// str_replace (s.replace(old, new)) — the last per-byte string builder,
			// Tier-2 via the raw-memory intrinsics (#2649). The old register-ABI
			// hand-asm (a bare __fern_str_replace: label + .Lrepl_walk loop) is
			// gone; the call site targets __fn___fern_str_replace via the stack ABI.
			"str_replace",
			`function main(): i32 { return "a.b".replace(".", "-").len(); }`,
			"__fn___fern_str_replace",
			[]string{"\n__fern_str_replace:", ".Lrepl_walk"},
		},
		{
			// string_from_bytes(arr) — pack each element's low byte into a string,
			// Tier-2 via the raw-memory intrinsics (#2649). The old register-ABI
			// hand-asm (a bare __fern_string_from_bytes: label + .Lsfb_loop) is gone;
			// the call site targets __fn___fern_string_from_bytes via the stack ABI.
			"string_from_bytes",
			`function main(): i32 { var b: u8[] = [104 as u8, 105 as u8]; return string_from_bytes(b).len(); }`,
			"__fn___fern_string_from_bytes",
			[]string{"\n__fern_string_from_bytes:", ".Lsfb_loop"},
		},
		{
			// str_split (s.split(sep) / str_split(s, sep)) — string[] result, built
			// with .append() + string slices (#2649). The old register-ABI hand-asm
			// (a bare __fern_str_split: label + .Lsplit_cl count loop) is gone; the
			// call site targets __fn___fern_str_split via the stack ABI.
			"str_split",
			`function main(): i32 { return "a,b,c".split(",").len(); }`,
			"__fn___fern_str_split",
			[]string{"\n__fern_str_split:", ".Lsplit_cl"},
		},
		{
			// arr_str_join (string[].join) — AST-only; calls str_concat via `+`.
			"arr_str_join",
			`function main(): i32 { var xs: string[] = ["a", "b"]; return xs.join(",").len(); }`,
			"__fn___fern_arr_str_join",
			[]string{"\n__fern_arr_str_join:", ".Lasj_loop"},
		},
		{
			// min/max lower on the IR path now (#3457), which calls the
			// Option-returning helper — the empty-array guard and the Option box
			// live in the Fern body rather than being open-coded at the call site
			// as the AST emitter does around the raw-extremum __fern_arr_i32_min.
			// Same migration contract: Fern-compiled `__fn_` symbol, hand-asm gone.
			"arr_i32_min",
			"function main(): i32 { var xs: i32[] = [5, 2, 7]; match (xs.min()) { Some(v) => { return v; }, None => { return 0; } } }",
			"__fn___fern_arr_i32_min_opt",
			[]string{"\n__fern_arr_i32_min:", ".Lai32_min_loop"},
		},
		{
			"arr_i32_max",
			"function main(): i32 { var xs: i32[] = [5, 2, 7]; match (xs.max()) { Some(v) => { return v; }, None => { return 0; } } }",
			"__fn___fern_arr_i32_max_opt",
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
