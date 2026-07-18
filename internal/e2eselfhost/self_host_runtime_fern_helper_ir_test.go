package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// Issue #2649 — IR-path runtime helpers written in Fern.
//
// __fern_str_to_i32 is the first runtime helper hosted on the self-hosted IR
// path as a Fern function (asmcore.rt_src_str_to_i32, lowered through the IR
// pipeline by asm_ir.emit_ir_runtime_fern_fn) rather than the hand-written
// stack-arg wrapper that used to live in emit_ir_runtime. It links as the
// ordinary user-function symbol __fn___fern_str_to_i32, which the IR call site
// (op_call_direct("__fern_str_to_i32") → ir_helper_symbol) already targets.
//
// TestSelfHostAsmIRPath/str2i32-* already prove the IR-compiled helper computes
// correctly (incl. the roundtrip case, which feeds a freshly-allocated string
// in — exercising the borrowed-param path with no use-after-free). This test
// locks in the *migration*: the IR driver's emitted asm must define the
// Fern-compiled __fn___fern_str_to_i32 and must NOT contain the old hand-asm
// wrapper's local labels (.Lirs2i_*), so a silent revert to the wrapper fails.
func TestSelfHostRuntimeHelperStrToI32IsFernIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun_rt")

	cases := []struct {
		name string
		prog string
		sym  string   // Fern-compiled symbol the IR asm must define + call
		gone []string // hand-asm labels of the old IR body/wrapper that must be gone
	}{
		{
			"str_to_i32",
			`function main(): i32 { return str_to_i32("42"); }`,
			"__fn___fern_str_to_i32",
			[]string{".Lirs2i_"},
		},
		{
			"str_cmp",
			`function main(): i32 { if ("abc" < "abd") { return 1; } return 0; }`,
			"__fn___fern_str_cmp",
			[]string{"\n__fern_str_cmp:", ".Lstrcmp_loop"},
		},
		{
			"str_search",
			`function main(): i32 { if ("hello".starts_with("he")) { return 1; } return 0; }`,
			"__fn___fern_str_starts_with",
			[]string{"\n__fern_str_starts_with:", ".Lir_sw_loop", ".Lir_idx_outer"},
		},
		{
			"str_eq",
			`function main(): i32 { if ("ab" == "ab") { return 1; } return 0; }`,
			"__fn___fern_str_eq",
			[]string{"\n__fern_str_eq:", ".Lstreq_loop"},
		},
		{
			// str_trim — migrated on the IR path too (the AST + IR str_trim both
			// became Fern). The old hand-written IR body's local labels (.Lir_trim_*)
			// must be gone.
			"str_trim",
			`function main(): i32 { return "  hi ".trim().len(); }`,
			"__fn___fern_str_trim",
			[]string{"\n__fern_str_trim:", ".Lir_trim_front", ".Ltrim_front"},
		},
		{
			// str_lines — migrated on the IR path too. The old hand-written IR body
			// (__fern_str_lines: / .Lir_lines_box) must be gone.
			"str_lines",
			`function main(): i32 { return "a\nb\n".lines().len(); }`,
			"__fn___fern_str_lines",
			[]string{"\n__fern_str_lines:", ".Lir_lines_box"},
		},
		{
			// str_bytes — migrated on the IR path too. The old hand-written IR body
			// (__fern_str_bytes: / .Lir_bytes_loop) must be gone.
			"str_bytes",
			`function main(): i32 { return "abc".bytes().len(); }`,
			"__fn___fern_str_bytes",
			[]string{"\n__fern_str_bytes:", ".Lir_bytes_loop"},
		},
		{
			// str_chars — migrated on the IR path too. The old hand-written IR body
			// (__fern_str_chars: / .Lir_chars_loop) must be gone.
			"str_chars",
			`function main(): i32 { return "abc".chars().len(); }`,
			"__fn___fern_str_chars",
			[]string{"\n__fern_str_chars:", ".Lir_chars_loop"},
		},
		{
			// chr — first Tier-2 helper via the raw-memory intrinsics (#2649). The IR
			// symbol __fn___fern_chr is unchanged, but the old hand-written stack-arg
			// body loaded its arg with `movq 8(%rsp), %rdi`; the Fern-compiled body
			// uses the standard frame, so that load must be gone.
			"chr",
			`function main(): i32 { return chr(65)[0]; }`,
			"__fn___fern_chr",
			[]string{"movq 8(%rsp), %rdi"},
		},
		{
			// str_concat — backs `+` on strings, migrated on the IR path too. The old
			// hand-written register-ABI body (__fern_str_concat: / .Lstrconcat_a_loop)
			// must be gone; the op now calls __fn___fern_str_concat.
			"str_concat",
			`function main(): i32 { var s: string = "ab" + "cd"; return s.len(); }`,
			"__fn___fern_str_concat",
			[]string{"\n__fern_str_concat:", ".Lstrconcat_a_loop"},
		},
		{
			// i32_to_string — migrated on the IR path too. The IR symbol
			// __fn___fern_i32_to_string is unchanged, but the old hand-written body's
			// loop label .Liri2s_div and stack-arg load `movq 8(%rsp), %rdi` are gone.
			"i32_to_string",
			`function main(): i32 { return i32_to_string(42).len(); }`,
			"__fn___fern_i32_to_string",
			[]string{".Liri2s_div", "movq 8(%rsp), %rdi"},
		},
		{
			// str_to_upper — migrated on the IR path too. The old hand-written IR
			// body (__fern_str_to_upper: / .Lir_upper_loop) must be gone.
			"str_to_upper",
			`function main(): i32 { return "aB".to_upper()[0]; }`,
			"__fn___fern_str_to_upper",
			[]string{"\n__fern_str_to_upper:", ".Lir_upper_loop"},
		},
		{
			// str_to_lower — the lower-case sibling on the IR path.
			"str_to_lower",
			`function main(): i32 { return "Ab".to_lower()[0]; }`,
			"__fn___fern_str_to_lower",
			[]string{"\n__fern_str_to_lower:", ".Lir_lower_loop"},
		},
		{
			// str_repeat — migrated on the IR path too (#2649). The old hand-written
			// IR body (__fern_str_repeat: / .Lir_rep_outer) must be gone; the
			// op_str_repeat handler now calls __fn___fern_str_repeat via the stack ABI.
			"str_repeat",
			`function main(): i32 { return "ab".repeat(3).len(); }`,
			"__fn___fern_str_repeat",
			[]string{"\n__fern_str_repeat:", ".Lir_rep_outer"},
		},
		{
			// str_reverse — migrated on the IR path too (#2649). The old hand-written
			// IR body (__fern_str_reverse: / .Lir_str_rev_loop) must be gone; the
			// op_str_reverse handler now calls __fn___fern_str_reverse via the stack ABI.
			"str_reverse",
			`function main(): i32 { return "abc".reverse()[0]; }`,
			"__fn___fern_str_reverse",
			[]string{"\n__fern_str_reverse:", ".Lir_str_rev_loop"},
		},
		{
			// str_replace — migrated on the IR path too (#2649). The old hand-written
			// IR body (__fern_str_replace: / .Lir_repl_walk) must be gone; the
			// op_str_replace handler now calls __fn___fern_str_replace via the stack ABI.
			"str_replace",
			`function main(): i32 { return "a.b".replace(".", "-").len(); }`,
			"__fn___fern_str_replace",
			[]string{"\n__fern_str_replace:", ".Lir_repl_walk"},
		},
		{
			// string_from_bytes — migrated on the IR path too (#2649). The old
			// hand-written IR body (__fern_string_from_bytes: / .Lir_sfb_loop) must
			// be gone; op_str_from_bytes now calls __fn___fern_string_from_bytes.
			"string_from_bytes",
			`function main(): i32 { var b: u8[] = [104 as u8, 105 as u8]; return string_from_bytes(b).len(); }`,
			"__fn___fern_string_from_bytes",
			[]string{"\n__fern_string_from_bytes:", ".Lir_sfb_loop"},
		},
		{
			// str_split — migrated on the IR path too (#2649). The old hand-written
			// IR body (__fern_str_split: / .Lir_split_cl) must be gone; op_str_split
			// now calls __fn___fern_str_split via the stack ABI.
			"str_split",
			`function main(): i32 { return "a,b,c".split(",").len(); }`,
			"__fn___fern_str_split",
			[]string{"\n__fern_str_split:", ".Lir_split_cl"},
		},
		{
			// random_bytes — the first syscall-leaf migrated to Fern (#2649),
			// over the __syscall3 sub-floor. The old hand-written IR body
			// (__fern_random_bytes:) must be gone; op_random_bytes now calls
			// __fn___fern_random_bytes via the stack ABI, whose body does the
			// getrandom syscall through the raw `syscall` the __syscall3 op emits.
			"random_bytes",
			`function main(): i32 { var b: string = random_bytes(8); return b.len(); }`,
			"__fn___fern_random_bytes",
			[]string{"\n__fern_random_bytes:"},
		},
		{
			// monotonic_ns — clock leaf migrated to Fern (#2649) over the
			// __syscall3 / __raw_scratch / __load_i64 floor. The old hand-asm
			// IR body (__fern_monotonic_ns:) must be gone; op_monotonic_ns now
			// calls __fn___fern_monotonic_ns.
			"monotonic_ns",
			`function main(): i32 { var a: i64 = monotonic_ns(); if (a > (0 as i64)) { return 1; } return 0; }`,
			"__fn___fern_monotonic_ns",
			[]string{"\n__fern_monotonic_ns:"},
		},
		{
			"now_unix_ms",
			`function main(): i32 { var a: i64 = now_unix_ms(); if (a > (0 as i64)) { return 1; } return 0; }`,
			"__fn___fern_now_unix_ms",
			[]string{"\n__fern_now_unix_ms:"},
		},
		{
			"now_ns",
			`function main(): i32 { var a: i64 = now_ns(); if (a > (0 as i64)) { return 1; } return 0; }`,
			"__fn___fern_now_ns",
			[]string{"\n__fern_now_ns:"},
		},
		{
			// stat — the first syscall leaf returning a user-typed
			// Result[FileStat, IoError] (#2649), migrated to Fern over the
			// __syscall3 / __raw_scratch / __load_i32/i64 floor. The old
			// hand-asm IR body (__fern_stat:) is gone; op_stat now calls
			// __fn___fern_stat, whose body builds Ok(FileStat{...}) /
			// Err(NotFound(_)) through the normal struct/enum lowering.
			"stat",
			`function main(): i32 { match (stat("/tmp")) { Ok(s) => { return 1; }, Err(e) => { return 0; } } }`,
			"__fn___fern_stat",
			[]string{"\n__fern_stat:"},
		},
		{
			// read_file — Result[string, IoError] leaf (#2649): the sized read
			// buffer becomes the Ok(string). The old hand-asm IR body
			// (__fern_read_file:) is gone; op_read_file calls __fn___fern_read_file.
			"read_file",
			`function main(): i32 { match (read_file("/nonexistent")) { Ok(s) => { return 1; }, Err(e) => { return 0; } } }`,
			"__fn___fern_read_file",
			[]string{"\n__fern_read_file:"},
		},
		{
			// remove_file — Option[IoError] leaf (#2649): unlinkat via __syscall3.
			// The old hand-asm IR body (__fern_remove_file:) is gone; op_remove_file
			// calls __fn___fern_remove_file.
			"remove_file",
			`function main(): i32 { match (remove_file("/nonexistent-xyz")) { None => { return 0; }, Some(e) => { return 1; } } }`,
			"__fn___fern_remove_file",
			[]string{"\n__fern_remove_file:"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.prog))
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver run: %v", err)
			}
			got := string(out)
			if !strings.Contains(got, tc.sym+":") {
				t.Errorf("IR asm missing %s: definition — helper no longer compiled from Fern on the IR path?", tc.sym)
			}
			if !strings.Contains(got, "call "+tc.sym) {
				t.Errorf("IR asm missing call %s — call site not resolving to the Fern helper", tc.sym)
			}
			for _, bad := range tc.gone {
				// Label patterns ("\n__fern_x:" definitions, ".L*" local
				// labels) are unique to the retired hand-written bodies, so
				// they scan the whole output. Generic instruction idioms
				// (the old stack-arg load `movq 8(%rsp), %rdi`) can
				// legitimately appear in OTHER hand-written runtime helpers
				// (#4551's __fn___fern_alloc_reuse uses the same load), so
				// they scan only the helper-under-test's own body — the
				// migration contract is that THIS symbol's body is
				// Fern-compiled, not that the idiom vanished globally.
				scope := got
				if !strings.HasPrefix(bad, "\n") && !strings.HasPrefix(bad, ".") {
					scope = extractFuncBody(got, tc.sym)
				}
				if strings.Contains(scope, bad) {
					t.Errorf("IR asm still contains hand-written form %q — IR migration regressed", bad)
				}
			}
		})
	}
}

// extractFuncBody returns the asm text of the named function: from its
// column-0 `sym:` label to the next column-0 function label (local `.L*:`
// labels inside the body don't terminate it). Empty when the label is
// missing — callers that need the definition assert it separately.
func extractFuncBody(asm, sym string) string {
	start := strings.Index(asm, "\n"+sym+":")
	if start < 0 {
		return ""
	}
	body := asm[start+1:]
	for off := len(sym) + 2; ; {
		next := strings.Index(body[off:], "\n")
		if next < 0 {
			return body
		}
		lineStart := off + next + 1
		rest := body[lineStart:]
		if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' && rest[0] != '.' && rest[0] != '\n' {
			if end := strings.Index(rest, "\n"); end < 0 || strings.Contains(rest[:end], ":") {
				return body[:lineStart]
			}
		}
		off = lineStart
	}
}
