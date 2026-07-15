package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stripLinesWithPrefix drops every line that starts with the given
// prefix. Used for normalised comparison of test output that contains
// run-to-run-varying lines (e.g. `# bench …` timing comments).
func stripLinesWithPrefix(s, prefix string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, ln := range lines {
		if strings.HasPrefix(ln, prefix) {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// TestSelfHostStdTestE2E proves the pure-Fern `std/test` runner works
// end-to-end through the self-hosted compiler: each example test file is
// compiled by the self-host file-loading driver (asm_load_run.fern, with
// the real repo stdlib as its import root), assembled, linked, and run —
// and its TAP-13 stdout + exit code must match the reference interpreter
// byte-for-byte. The interpreter is the oracle, so the gate tracks the
// examples as they evolve rather than pinning hand-copied output.
//
// Native only: the driver reads stdlib module files by host path from
// argv, so a qemu runner couldn't resolve them (mirrors the local
// file-loading + stdlib-import tests). The self-host print=println
// codegen exercised here is covered on arm64 by the CI-gated f64 and
// asm-emit suites.
func TestSelfHostStdTestE2E(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)

	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"flatten.fern", "checker.fern", "util.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// A synthetic failing suite (written to a temp file) pins the
	// failure path: a `not ok` line and exit code 1. The example files
	// below are all green suites (exit 0).
	failing := filepath.Join(t.TempDir(), "synthetic_fail_test.fern")
	failSrc := "import \"std/test\";\n" +
		"function bad(): test.TestOutcome { return test.assert_eq(1, 2); }\n" +
		"function main(): i32 {\n" +
		"    var r: test.TestRunner = test.test_new(\"synthetic\");\n" +
		"    r = r.it(\"one is two\", bad());\n" +
		"    return r.finish();\n" +
		"}\n"
	if err := os.WriteFile(failing, []byte(failSrc), 0o644); err != nil {
		t.Fatalf("write synthetic: %v", err)
	}

	cases := selfHostStdTestCases(t, failing)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Oracle: the reference interpreter.
			ic := exec.Command(interpBin, "-interp", tc.src)
			wantOut, _ := ic.Output()
			wantExit := ic.ProcessState.ExitCode()

			// Self-host: compile → assemble → link → run.
			asm, err := exec.Command(mmc, tc.src, stdlibRoot).Output()
			if err != nil {
				t.Fatalf("self-host compile failed: %v", err)
			}
			if len(asm) == 0 {
				t.Fatal("self-host emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			rc := exec.Command(bin)
			gotOut, _ := rc.Output()
			gotExit := rc.ProcessState.ExitCode()

			if gotExit != wantExit {
				t.Errorf("exit code: self-host %d, interp %d", gotExit, wantExit)
			}
			gotStr := string(gotOut)
			wantStr := string(wantOut)
			if tc.stripPrefix != "" {
				gotStr = stripLinesWithPrefix(gotStr, tc.stripPrefix)
				wantStr = stripLinesWithPrefix(wantStr, tc.stripPrefix)
			}
			if gotStr != wantStr {
				t.Errorf("TAP output mismatch:\n--- self-host ---\n%s\n--- interp ---\n%s", gotStr, wantStr)
			}
		})
	}
}

// TestSelfHostStdTestE2EArm64 is the arm64 mirror of the gate above.
// Gates the arm64 self-host emitter (`asm_arm64.fern`): mmc is built
// from `asm_load_run.fern -target arm64` as a native x86 host binary (the
// same cross-compiler-on-host pattern the existing arm64 reader /
// alloc-trap tests use), then for each case the host mmc emits
// aarch64 assembly, the aarch64 cross-gcc assembles + links, and
// qemu-aarch64 runs the resulting binary — stdout + exit code must
// match the reference interpreter byte-for-byte. On native arm64
// hardware qemu is empty and the binary runs directly.
//
// Same case list as the x86 gate so arm64 emitter regressions
// surface on every PR — without this, parity work (e.g. the
// unsigned-cmp / wider-int dispatch mirror) only shows up when
// someone runs the arm64 emit suite manually.
func TestSelfHostStdTestE2EArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 differential gate needs a native x86 host to run the driver")
	}
	interpBin := buildLangBinForInterp(t)

	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "treeshake.fern", "asm.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Build the arm64 driver as a native x86 host binary — its
	// OUTPUT is aarch64 asm. Cheaper than running mmc itself under
	// qemu and avoids any arm64-self-compiling-arm64-self bugs in
	// the Go arm64 backend that aren't part of what this gate is
	// trying to validate.
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_arm64")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	failing := filepath.Join(t.TempDir(), "synthetic_fail_test.fern")
	failSrc := "import \"std/test\";\n" +
		"function bad(): test.TestOutcome { return test.assert_eq(1, 2); }\n" +
		"function main(): i32 {\n" +
		"    var r: test.TestRunner = test.test_new(\"synthetic\");\n" +
		"    r = r.it(\"one is two\", bad());\n" +
		"    return r.finish();\n" +
		"}\n"
	if err := os.WriteFile(failing, []byte(failSrc), 0o644); err != nil {
		t.Fatalf("write synthetic: %v", err)
	}

	cases := selfHostStdTestCases(t, failing)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Oracle: the reference interpreter (native x86 binary).
			ic := exec.Command(interpBin, "-interp", tc.src)
			wantOut, _ := ic.Output()
			wantExit := ic.ProcessState.ExitCode()

			// Self-host: native x86 mmc emits aarch64 asm; gcc-
			// aarch64 assembles + links; qemu-aarch64 runs (or
			// native, when qemu == "").
			asm, err := exec.Command(mmc, tc.src, stdlibRoot, "-target", "arm64").Output()
			if err != nil {
				t.Fatalf("self-host compile failed: %v", err)
			}
			if len(asm) == 0 {
				t.Fatal("self-host emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			gotOut, _ := runArm64Bin(qemu, bin).Output()
			rc := runArm64Bin(qemu, bin)
			_ = rc.Run()
			gotExit := rc.ProcessState.ExitCode()

			if gotExit != wantExit {
				t.Errorf("exit code: self-host %d, interp %d", gotExit, wantExit)
			}
			gotStr := string(gotOut)
			wantStr := string(wantOut)
			if tc.stripPrefix != "" {
				gotStr = stripLinesWithPrefix(gotStr, tc.stripPrefix)
				wantStr = stripLinesWithPrefix(wantStr, tc.stripPrefix)
			}
			if gotStr != wantStr {
				t.Errorf("TAP output mismatch:\n--- self-host ---\n%s\n--- interp ---\n%s", gotStr, wantStr)
			}
		})
	}
}

type selfHostStdTestCase struct {
	name        string
	src         string
	stripPrefix string
}

// selfHostStdTestCases returns the differential gate case list shared
// by both backend variants. Kept in sync with the case slice
// constructed in TestSelfHostStdTestE2E — when a new gate case lands
// there, mirror it here so the arm64 variant picks it up too.
func selfHostStdTestCases(t *testing.T, failing string) []selfHostStdTestCase {
	t.Helper()
	return []selfHostStdTestCase{
		{"arithmetic", langSrcAbs(t, "examples/tests/arithmetic_test.fern"), ""},
		{"strings", langSrcAbs(t, "examples/tests/strings_test.fern"), ""},
		{"fail_fast", langSrcAbs(t, "examples/tests/fail_fast_test.fern"), ""},
		{"quiet_mode", langSrcAbs(t, "examples/tests/quiet_mode_test.fern"), ""},
		{"skip_and_subsuites", langSrcAbs(t, "examples/tests/skip_and_subsuites_test.fern"), ""},
		{"runner_self", langSrcAbs(t, "examples/tests/runner_self_test.fern"), ""},
		{"option_and_set_ops", langSrcAbs(t, "examples/tests/option_and_set_ops_test.fern"), ""},
		{"result_assertions", langSrcAbs(t, "examples/tests/result_assertions_test.fern"), ""},
		{"fuzz_example", langSrcAbs(t, "examples/tests/fuzz_example_test.fern"), ""},
		{"fuzz_corpus", langSrcAbs(t, "examples/tests/fuzz_corpus_test.fern"), ""},
		{"fuzz_shrink", langSrcAbs(t, "examples/tests/fuzz_shrink_test.fern"), ""},
		{"filesystem_ops", langSrcAbs(t, "examples/tests/filesystem_ops_test.fern"), ""},
		{"float_math", langSrcAbs(t, "examples/tests/float_math_test.fern"), ""},
		{"float_convert", langSrcAbs(t, "examples/tests/float_convert_test.fern"), ""},
		{"float_hypot", langSrcAbs(t, "examples/tests/float_hypot_test.fern"), ""},
		{"float_round_to", langSrcAbs(t, "examples/tests/float_round_to_test.fern"), ""},
		{"float_log2_log10", langSrcAbs(t, "examples/tests/float_log2_log10_test.fern"), ""},
		{"float_exp2_exp10", langSrcAbs(t, "examples/tests/float_exp2_exp10_test.fern"), ""},
		{"float_recip_copysign_midpoint", langSrcAbs(t, "examples/tests/float_recip_copysign_midpoint_test.fern"), ""},
		{"i64_roots", langSrcAbs(t, "examples/tests/i64_roots_test.fern"), ""},
		{"i64_intdiv", langSrcAbs(t, "examples/tests/i64_intdiv_test.fern"), ""},
		{"u64_roots", langSrcAbs(t, "examples/tests/u64_roots_test.fern"), ""},
		{"float_clamp01_absdiff_muladd", langSrcAbs(t, "examples/tests/float_clamp01_absdiff_muladd_test.fern"), ""},
		{"float_cbrt_hypot3", langSrcAbs(t, "examples/tests/float_cbrt_hypot3_test.fern"), ""},
		{"float_hyperbolic", langSrcAbs(t, "examples/tests/float_hyperbolic_test.fern"), ""},
		// NOTE: u32_roots is deliberately NOT gated here. It is native-differential
		// + interp only: the self-host compiler's u32 arithmetic doesn't truncate /
		// compare as unsigned near the 2^31 boundary (the documented u32 self-host
		// gap that keeps u32_arith interp-only), which makes next_power_of_2's
		// doubling loop spin forever in the self-host-compiled binary. All four
		// native backends handle it correctly — see internal/e2e/u32_roots_test.go.
		{"float_array_strict_sort", langSrcAbs(t, "examples/tests/float_array_strict_sort_test.fern"), ""},
		{"lines_log", langSrcAbs(t, "examples/tests/lines_log_test.fern"), ""},
		{"assert_at_wider", langSrcAbs(t, "examples/tests/assert_at_wider_test.fern"), ""},
		{"array_at_and_f32_range", langSrcAbs(t, "examples/tests/array_at_and_f32_range_test.fern"), ""},
		{"map_eq_and_predicates", langSrcAbs(t, "examples/tests/map_eq_and_predicates_test.fern"), ""},
		{"derive", langSrcAbs(t, "examples/tests/derive_test.fern"), ""},
		{"json_field_eq", langSrcAbs(t, "examples/tests/json_field_eq_test.fern"), ""},
		{"header_map_migrated", langSrcAbs(t, "examples/tests/header_map_migrated_test.fern"), ""},
		{"http_request_headers_migrated", langSrcAbs(t, "examples/tests/http_request_headers_migrated_test.fern"), ""},
		{"http_response_headers_migrated", langSrcAbs(t, "examples/tests/http_response_headers_migrated_test.fern"), ""},
		{"string_prelude_migrated", langSrcAbs(t, "examples/tests/string_prelude_migrated_test.fern"), ""},
		{"bench", langSrcAbs(t, "examples/tests/bench_test.fern"), "# bench "},
		{"rel_tol_and_ms_bench", langSrcAbs(t, "examples/tests/rel_tol_and_ms_bench_test.fern"), "# bench "},
		{"batch8", langSrcAbs(t, "examples/tests/batch8_test.fern"), "# golden file bootstrapped at "},
		{"process_assertions", langSrcAbs(t, "examples/tests/process_assertions_test.fern"), ""},
		{"process_output_shortcuts", langSrcAbs(t, "examples/tests/process_output_shortcuts_test.fern"), ""},
		{"lang_binary_e2e", langSrcAbs(t, "examples/tests/lang_binary_e2e_test.fern"), ""},
		{"sort_wider", langSrcAbs(t, "examples/tests/sort_wider_test.fern"), ""},
		{"array_reductions", langSrcAbs(t, "examples/tests/array_reductions_test.fern"), ""},
		{"array_structural_verbs", langSrcAbs(t, "examples/tests/array_structural_verbs_test.fern"), ""},
		{"log", langSrcAbs(t, "examples/tests/log_test.fern"), ""},
		{"wide_numerics", langSrcAbs(t, "examples/tests/wide_numerics_test.fern"), ""},
		{"wider_array_contains_count", langSrcAbs(t, "examples/tests/wider_array_contains_count_test.fern"), ""},
		{"sorted_unique_range", langSrcAbs(t, "examples/tests/sorted_unique_range_test.fern"), ""},
		{"all_substring_array", langSrcAbs(t, "examples/tests/all_substring_array_test.fern"), ""},
		{"array_prefix_suffix_subseq", langSrcAbs(t, "examples/tests/array_prefix_suffix_subseq_test.fern"), ""},
		{"batch7", langSrcAbs(t, "examples/tests/batch7_test.fern"), ""},
		{"io_buffered", langSrcAbs(t, "examples/tests/io_buffered_test.fern"), ""},
		{"iter", langSrcAbs(t, "examples/tests/iter_test.fern"), ""},
		{"ci_string_and_log_kv", langSrcAbs(t, "examples/tests/ci_string_and_log_kv_test.fern"), ""},
		{"env_unreachable", langSrcAbs(t, "examples/tests/env_unreachable_test.fern"), ""},
		{"file_lines_and_timestamp", langSrcAbs(t, "examples/tests/file_lines_and_timestamp_test.fern"), ""},
		{"float", langSrcAbs(t, "examples/tests/float_test.fern"), ""},
		{"helpers", langSrcAbs(t, "examples/tests/helpers_test.fern"), ""},
		{"json_detail", langSrcAbs(t, "examples/tests/json_detail_test.fern"), ""},
		{"one_of_none_of", langSrcAbs(t, "examples/tests/one_of_none_of_test.fern"), ""},
		{"set_eq", langSrcAbs(t, "examples/tests/set_eq_test.fern"), ""},
		{"string_count_and_dir_listing", langSrcAbs(t, "examples/tests/string_count_and_dir_listing_test.fern"), ""},
		{"timing", langSrcAbs(t, "examples/tests/timing_test.fern"), ""},
		{"unions_migrated", langSrcAbs(t, "examples/tests/unions_migrated_test.fern"), ""},
		{"math", langSrcAbs(t, "examples/tests/math_test.fern"), ""},
		{"path", langSrcAbs(t, "examples/tests/path_test.fern"), ""},
		{"hex", langSrcAbs(t, "examples/tests/hex_test.fern"), ""},
		{"base64", langSrcAbs(t, "examples/tests/base64_test.fern"), ""},
		{"url", langSrcAbs(t, "examples/tests/url_test.fern"), ""},
		{"cli", langSrcAbs(t, "examples/tests/cli_test.fern"), ""},
		{"format", langSrcAbs(t, "examples/tests/format_test.fern"), ""},
		{"csv", langSrcAbs(t, "examples/tests/csv_test.fern"), ""},
		{"int", langSrcAbs(t, "examples/tests/int_test.fern"), ""},
		{"i32", langSrcAbs(t, "examples/tests/i32_test.fern"), ""},
		{"i32_bit_arith", langSrcAbs(t, "examples/tests/i32_bit_arith_test.fern"), ""},
		{"i32_to_string_radix", langSrcAbs(t, "examples/tests/i32_to_string_radix_test.fern"), ""},
		{"i32_bit_length", langSrcAbs(t, "examples/tests/i32_bit_length_test.fern"), ""},
		{"i64", langSrcAbs(t, "examples/tests/i64_test.fern"), ""},
		{"i64_range", langSrcAbs(t, "examples/tests/i64_range_test.fern"), ""},
		{"i64_bit_ops", langSrcAbs(t, "examples/tests/i64_bit_ops_test.fern"), ""},
		{"i64_to_string_radix", langSrcAbs(t, "examples/tests/i64_to_string_radix_test.fern"), ""},
		// u64 routes IR now that mono_infer types an `as u64` cast arg, so a
		// bounded-generic assert_eq(x.min(y), v as u64) binds T=u64 (#3457).
		{"u64", langSrcAbs(t, "examples/tests/u64_test.fern"), ""},
		{"uuid", langSrcAbs(t, "examples/tests/uuid_test.fern"), ""},
		{"json_roundtrip", langSrcAbs(t, "examples/tests/json_roundtrip_test.fern"), ""},
		{"json_pointer", langSrcAbs(t, "examples/tests/json_pointer_test.fern"), ""},
		{"array_combinators", langSrcAbs(t, "examples/tests/array_combinators_test.fern"), ""},
		// Generic array CLOSURE-methods (.reduce / .flat_map / .sort_by / .map[U] /
		// .fold[A]) flipped to IR by the __arrm_* free-generic rewrite (slices 3+4,
		// #3976/#3977); pin the whole 8-test suite on the differential gate so the
		// IR routing can't silently regress (it was previously only covered by the
		// synthetic single-function closure/typaram IR tests).
		{"array_hof", langSrcAbs(t, "examples/tests/array_hof_test.fern"), ""},
		{"array_accessors", langSrcAbs(t, "examples/tests/array_accessors_test.fern"), ""},
		{"array_dedup", langSrcAbs(t, "examples/tests/array_dedup_test.fern"), ""},
		{"array_binary_search", langSrcAbs(t, "examples/tests/array_binary_search_test.fern"), ""},
		{"array_min_max_index", langSrcAbs(t, "examples/tests/array_min_max_index_test.fern"), ""},
		{"array_all_equal", langSrcAbs(t, "examples/tests/array_all_equal_test.fern"), ""},
		{"array_none", langSrcAbs(t, "examples/tests/array_none_test.fern"), ""},
		{"array_rotate", langSrcAbs(t, "examples/tests/array_rotate_test.fern"), ""},
		{"array_batch", langSrcAbs(t, "examples/tests/array_batch_test.fern"), ""},
		{"iter_combinators", langSrcAbs(t, "examples/tests/iter_combinators_test.fern"), ""},
		{"num_reducers", langSrcAbs(t, "examples/tests/num_reducers_test.fern"), ""},
		{"time_iso_span", langSrcAbs(t, "examples/tests/time_iso_span_test.fern"), ""},
		{"time_calendar", langSrcAbs(t, "examples/tests/time_calendar_test.fern"), ""},
		{"time_http_date", langSrcAbs(t, "examples/tests/time_http_date_test.fern"), ""},
		{"time_timezone", langSrcAbs(t, "examples/tests/time_timezone_test.fern"), ""},
		{"string_replace_split", langSrcAbs(t, "examples/tests/string_replace_split_test.fern"), ""},
		{"string_rsplit_once", langSrcAbs(t, "examples/tests/string_rsplit_once_test.fern"), ""},
		{"string_partition", langSrcAbs(t, "examples/tests/string_partition_test.fern"), ""},
		{"string_parse_radix", langSrcAbs(t, "examples/tests/string_parse_radix_test.fern"), ""},
		{"string_zfill", langSrcAbs(t, "examples/tests/string_zfill_test.fern"), ""},
		{"string_swap_case", langSrcAbs(t, "examples/tests/string_swap_case_test.fern"), ""},
		{"utf8", langSrcAbs(t, "examples/tests/utf8_test.fern"), ""},
		{"string_escape_count", langSrcAbs(t, "examples/tests/string_escape_count_test.fern"), ""},
		{"string_slice_extract", langSrcAbs(t, "examples/tests/string_slice_extract_test.fern"), ""},
		{"string_classify_transform", langSrcAbs(t, "examples/tests/string_classify_transform_test.fern"), ""},
		{"sort_by_and_ci", langSrcAbs(t, "examples/tests/sort_by_and_ci_test.fern"), ""},
		{"option_combinators", langSrcAbs(t, "examples/tests/option_combinators_test.fern"), ""},
		{"result_combinators", langSrcAbs(t, "examples/tests/result_combinators_test.fern"), ""},
		// rc_struct_drop exercises __struct_drop_<T> deep-drop of a reclaimable
		// struct's rc-array fields (scalar-array k_scalar + struct-array k_box
		// paths) over many alloc→drop cycles. Differential vs interp catches a
		// broken drop on x86-64 AND arm64 — the latter now a real deep-drop
		// (Perceus self-host slice 1a) rather than a leak-safe pass-through.
		{"rc_struct_drop", langSrcAbs(t, "examples/tests/rc_struct_drop_test.fern"), ""},
		// map_verbs flips to IR now that generic map verbs monomorphise: the
		// methods (merge/extend/get_or_insert/entries/…) via the __mapm_ fold
		// (#4016), and the free `from[K,V](pairs: (K,V)[])` via promoting a
		// type-var that feeds a Map[...] position + bind_unify destructuring the
		// `(K,V)[]` tuple arg + mono_infer typing the tuple literal. The last
		// non-async AST-router (#3457).
		{"map_verbs", langSrcAbs(t, "examples/tests/map_verbs_test.fern"), ""},
		// crypto + u32 lower fully on the IR path now that remove_dir_all (the
		// TestRunner.finish cleanup call) lowers there — previously the bail
		// dragged every std/test module onto the AST emitter, whose u32
		// arithmetic doesn't truncate to 32 bits, miscompiling SHA-256 (#3457).
		{"u32_arith", langSrcAbs(t, "examples/tests/u32_arith_test.fern"), ""},
		{"crypto", langSrcAbs(t, "examples/tests/crypto_test.fern"), ""},
		{"synthetic_fail", failing, ""},
	}
}

// buildBinArm64 assembles+links arm64 asm into dir/name and returns
// its path. Drops the x86-specific `-no-pie` flag (some aarch64 gcc
// builds reject it).
func buildBinArm64(t *testing.T, gcc, dir, name, asm string) string {
	t.Helper()
	asmPath := filepath.Join(dir, name+".s")
	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write %s asm: %v", name, err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc %s: %v\n%s", name, err, out)
	}
	return binPath
}
