package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
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
// The configured user-mode runner shares the driver's filesystem paths.
func TestSelfHostStdTestE2E(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../stdlib")
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
		"    let r: test.TestRunner = test.test_new(\"synthetic\");\n" +
		"    r = r.it(\"one is two\", bad);\n" +
		"    return r.finish();\n" +
		"}\n"
	if err := os.WriteFile(failing, []byte(failSrc), 0o644); err != nil {
		t.Fatalf("write synthetic: %v", err)
	}

	cases := selfHostStdTestCases(t, failing)

	// The group includes parallel children in the top-level duration used
	// by scripts/ci-test-weights. Each case owns its output directory.
	t.Run("cases", func(t *testing.T) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				caseDir := t.TempDir()
				// Oracle: the reference interpreter.
				ic := exec.Command(interpBin, "-interp", tc.src)
				wantOut, _ := stdTestOutput(ic)
				wantExit := ic.ProcessState.ExitCode()

				// Self-host: compile → assemble → link → run.
				asm, err := stdTestOutput(runX86_64Bin(runner, mmc, tc.src, stdlibRoot))
				if err != nil {
					// The driver reports the reason (a checker diagnostic, an
					// unsupported construct) on stderr; without it the failure is
					// a bare "exit status 1" and the next reader has to rebuild
					// the driver by hand to learn anything.
					var ee *exec.ExitError
					if errors.As(err, &ee) {
						t.Fatalf("self-host compile failed: %v\n%s", err, ee.Stderr)
					}
					t.Fatalf("self-host compile failed: %v", err)
				}
				if len(asm) == 0 {
					t.Fatal("self-host emitted 0 bytes")
				}
				bin := buildBin(t, gcc, caseDir, tc.name, string(asm))
				rc := runX86_64Bin(runner, bin)
				gotOut, _ := stdTestOutput(rc)
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
	})
}

// TestSelfHostStdTestE2EArm64 is the arm64 mirror of the gate above.
// Gates the arm64 self-host IR emitter: mmc is built
// from `asm_load_run.fern -target arm64-linux` as an x86 driver binary (the
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
	interpBin := buildLangBinForInterp(t)

	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	// The x86 driver emits ARM64 assembly, independently of the host target.
	mmc := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "mmc_arm64")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	failing := filepath.Join(t.TempDir(), "synthetic_fail_test.fern")
	failSrc := "import \"std/test\";\n" +
		"function bad(): test.TestOutcome { return test.assert_eq(1, 2); }\n" +
		"function main(): i32 {\n" +
		"    let r: test.TestRunner = test.test_new(\"synthetic\");\n" +
		"    r = r.it(\"one is two\", bad);\n" +
		"    return r.finish();\n" +
		"}\n"
	if err := os.WriteFile(failing, []byte(failSrc), 0o644); err != nil {
		t.Fatalf("write synthetic: %v", err)
	}

	cases := selfHostStdTestCases(t, failing)
	// The group includes parallel children in the top-level duration used
	// by scripts/ci-test-weights. Each case owns its output directory.
	t.Run("cases", func(t *testing.T) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				caseDir := t.TempDir()
				// Oracle: the reference interpreter built for this host.
				ic := exec.Command(interpBin, "-interp", tc.src)
				wantOut, _ := stdTestOutput(ic)
				wantExit := ic.ProcessState.ExitCode()

				// Self-host: x86 mmc emits aarch64 asm; gcc-
				// aarch64 assembles + links; qemu-aarch64 runs (or
				// native, when qemu == "").
				asm, err := stdTestOutput(runX86_64Bin(x86runner, mmc, tc.src, stdlibRoot, "-target", "arm64-linux"))
				if err != nil {
					// Same reason the x86-64 sibling above prints stderr: without
					// it the failure is a bare "exit status 1" and the next reader
					// has to rebuild the driver by hand to learn anything.
					var ee *exec.ExitError
					if errors.As(err, &ee) {
						t.Fatalf("self-host compile failed: %v\n%s", err, ee.Stderr)
					}
					t.Fatalf("self-host compile failed: %v", err)
				}
				if len(asm) == 0 {
					t.Fatal("self-host emitted 0 bytes")
				}
				bin := buildBinArm64(t, arm64gcc, caseDir, tc.name, string(asm))
				rc := runArm64Bin(qemu, bin)
				gotOut, _ := stdTestOutput(rc)
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
	})
}

// stdTestOutput budgets each subprocess separately. Linking has its own
// reservation, so holding this across buildBin would nest reservations and
// could deadlock. The full native corpus peaked at 4.25 GB for a single
// child; reserve 5 GiB to leave room above that measurement.
func stdTestOutput(cmd *exec.Cmd) ([]byte, error) {
	var out []byte
	err := e2eharness.WithBuildMemoryMB(5*1024, func() error {
		var err error
		out, err = cmd.Output()
		return err
	})
	return out, err
}

type selfHostStdTestCase struct {
	name        string
	src         string
	stripPrefix string
}

// selfHostStdTestCases returns the differential gate case list shared
// by both backend variants. It is the single list: TestSelfHostStdTestE2E
// calls it too, so a case added here reaches every variant.
func selfHostStdTestCases(t *testing.T, failing string) []selfHostStdTestCase {
	t.Helper()
	return []selfHostStdTestCase{
		{"arithmetic", langSrcAbs(t, "tests/stdlib/arithmetic_test.fern"), ""},
		{"strings", langSrcAbs(t, "tests/stdlib/strings_test.fern"), ""},
		{"dns", langSrcAbs(t, "tests/stdlib/dns_test.fern"), ""},
		{"fail_fast", langSrcAbs(t, "tests/stdlib/fail_fast_test.fern"), ""},
		{"quiet_mode", langSrcAbs(t, "tests/stdlib/quiet_mode_test.fern"), ""},
		{"skip_and_subsuites", langSrcAbs(t, "tests/stdlib/skip_and_subsuites_test.fern"), ""},
		{"runner_self", langSrcAbs(t, "tests/stdlib/runner_self_test.fern"), ""},
		// core/cmp's six impls for u8 (Display/Eq/Ord/Hash/Debug/Default).
		// Routed through the self-host deliberately: the first four were
		// removed in #5869 because the self-host collapsed u8 onto the i32
		// tag and the impl sets collided (an E021 differential). The native
		// side would not have caught that.
		{"u8_traits", langSrcAbs(t, "tests/stdlib/u8_traits_test.fern"), ""},
		{"option_and_set_ops", langSrcAbs(t, "tests/stdlib/option_and_set_ops_test.fern"), ""},
		{"result_assertions", langSrcAbs(t, "tests/stdlib/result_assertions_test.fern"), ""},
		{"fuzz_example", langSrcAbs(t, "tests/stdlib/fuzz_example_test.fern"), ""},
		{"fuzz_corpus", langSrcAbs(t, "tests/stdlib/fuzz_corpus_test.fern"), ""},
		{"fuzz_shrink", langSrcAbs(t, "tests/stdlib/fuzz_shrink_test.fern"), ""},
		{"filesystem_ops", langSrcAbs(t, "tests/stdlib/filesystem_ops_test.fern"), ""},
		{"enum_payload_sort_rc", langSrcAbs(t, "tests/stdlib/enum_payload_sort_rc_test.fern"), ""},
		// The persistent collections (#6794): generic enum trees reached
		// through generic-struct methods and bounded free generics — the
		// shape the self-host monomorphiser erased to `__K__V` clones before
		// it was taught to instantiate them concretely.
		{"ordmap", langSrcAbs(t, "tests/stdlib/ordmap_test.fern"), ""},
		{"ordset", langSrcAbs(t, "tests/stdlib/ordset_test.fern"), ""},
		{"pmap", langSrcAbs(t, "tests/stdlib/pmap_test.fern"), ""},
		{"pset", langSrcAbs(t, "tests/stdlib/pset_test.fern"), ""},
		{"pvec", langSrcAbs(t, "tests/stdlib/pvec_test.fern"), ""},
		{"float_math", langSrcAbs(t, "tests/stdlib/float_math_test.fern"), ""},
		{"float_convert", langSrcAbs(t, "tests/stdlib/float_convert_test.fern"), ""},
		{"float_hypot", langSrcAbs(t, "tests/stdlib/float_hypot_test.fern"), ""},
		{"float_round_to", langSrcAbs(t, "tests/stdlib/float_round_to_test.fern"), ""},
		{"float_log2_log10", langSrcAbs(t, "tests/stdlib/float_log2_log10_test.fern"), ""},
		{"float_exp2_exp10", langSrcAbs(t, "tests/stdlib/float_exp2_exp10_test.fern"), ""},
		{"float_recip_copysign_midpoint", langSrcAbs(t, "tests/stdlib/float_recip_copysign_midpoint_test.fern"), ""},
		{"i64_roots", langSrcAbs(t, "tests/stdlib/i64_roots_test.fern"), ""},
		{"i64_intdiv", langSrcAbs(t, "tests/stdlib/i64_intdiv_test.fern"), ""},
		{"i32_roots_prime", langSrcAbs(t, "tests/stdlib/i32_roots_prime_test.fern"), ""},
		{"u64_roots", langSrcAbs(t, "tests/stdlib/u64_roots_test.fern"), ""},
		{"float_clamp01_absdiff_muladd", langSrcAbs(t, "tests/stdlib/float_clamp01_absdiff_muladd_test.fern"), ""},
		{"float_cbrt_hypot3", langSrcAbs(t, "tests/stdlib/float_cbrt_hypot3_test.fern"), ""},
		{"float_hyperbolic", langSrcAbs(t, "tests/stdlib/float_hyperbolic_test.fern"), ""},
		{"array_stats", langSrcAbs(t, "tests/stdlib/array_stats_test.fern"), ""},
		{"sort_f64", langSrcAbs(t, "tests/stdlib/sort_f64_test.fern"), ""},
		{"array_median_range", langSrcAbs(t, "tests/stdlib/array_median_range_test.fern"), ""},
		{"array_vector", langSrcAbs(t, "tests/stdlib/array_vector_test.fern"), ""},
		{"array_distance_normalize", langSrcAbs(t, "tests/stdlib/array_distance_normalize_test.fern"), ""},
		{"array_product_cumsum", langSrcAbs(t, "tests/stdlib/array_product_cumsum_test.fern"), ""},
		{"array_scale_add", langSrcAbs(t, "tests/stdlib/array_scale_add_test.fern"), ""},
		{"array_cumprod_diff", langSrcAbs(t, "tests/stdlib/array_cumprod_diff_test.fern"), ""},
		{"int_midpoint", langSrcAbs(t, "tests/stdlib/int_midpoint_test.fern"), ""},
		// uint_midpoint + u32_roots are now self-host gated. Both were formerly
		// interp-only, blamed on a "u32 self-host gap" (an arithmetic/sign-extending
		// `>>` for u32, plus a next_power_of_2 doubling loop that spun forever). The
		// real cause was a single mis-dispatch bug: irlower's expr_recv_prim_type had
		// no u32 branch, so a u32-receiver method call keyed "i32.<method>" and ran
		// the SIGNED std/i32 method (signed compare + arithmetic shift) instead of
		// the unsigned std/u32 one. Fixed in expr_recv_prim_type; both now match the
		// interpreter byte-for-byte on x86-64, arm64, and wasm IR.
		{"uint_midpoint", langSrcAbs(t, "tests/stdlib/uint_midpoint_test.fern"), ""},
		{"u32_roots", langSrcAbs(t, "tests/stdlib/u32_roots_test.fern"), ""},
		{"float_array_strict_sort", langSrcAbs(t, "tests/stdlib/float_array_strict_sort_test.fern"), ""},
		{"lines_log", langSrcAbs(t, "tests/stdlib/lines_log_test.fern"), ""},
		{"assert_at_wider", langSrcAbs(t, "tests/stdlib/assert_at_wider_test.fern"), ""},
		{"array_at_and_f32_range", langSrcAbs(t, "tests/stdlib/array_at_and_f32_range_test.fern"), ""},
		{"map_eq_and_predicates", langSrcAbs(t, "tests/stdlib/map_eq_and_predicates_test.fern"), ""},
		{"derive", langSrcAbs(t, "tests/stdlib/derive_test.fern"), ""},
		{"json_field_eq", langSrcAbs(t, "tests/stdlib/json_field_eq_test.fern"), ""},
		{"header_map_migrated", langSrcAbs(t, "tests/stdlib/header_map_migrated_test.fern"), ""},
		{"http_request_headers_migrated", langSrcAbs(t, "tests/stdlib/http_request_headers_migrated_test.fern"), ""},
		{"http_request_bytes", langSrcAbs(t, "tests/stdlib/http_request_bytes_test.fern"), ""},
		{"http_request_builder", langSrcAbs(t, "tests/stdlib/http_request_builder_test.fern"), ""},
		{"http_respond", langSrcAbs(t, "tests/stdlib/http_respond_test.fern"), ""},
		{"mock_platform_canned", langSrcAbs(t, "tests/stdlib/mock_platform_canned_test.fern"), ""},
		// std/fetch's client over the scripted network: the dialled route's
		// sim parity suite, behind the generic fetch.Transport seam.
		{"sim_fetch", langSrcAbs(t, "tests/stdlib/sim_fetch_test.fern"), ""},
		{"sim_net", langSrcAbs(t, "tests/stdlib/sim_net_test.fern"), ""},
		{"sim_fault", langSrcAbs(t, "tests/stdlib/sim_fault_test.fern"), ""},
		// A handler's platform over the simulation: the platform's sim parity
		// suite.
		{"sim_platform", langSrcAbs(t, "tests/stdlib/sim_platform_test.fern"), ""},
		// The future combinators over Ready futures: the same TAP output
		// under the self-host's suspension pass as under the interpreter.
		{"async_combinators", langSrcAbs(t, "tests/stdlib/async_combinators_test.fern"), ""},
		{"http_body", langSrcAbs(t, "tests/stdlib/http_body_test.fern"), ""},
		// HttpRequest.body as a Stream, in memory and pulled from a source
		// (a closure over cells), the shape a streamed request body has.
		{"http_request_body_stream", langSrcAbs(t, "tests/stdlib/http_request_body_stream_test.fern"), ""},
		{"http_body_json", langSrcAbs(t, "tests/stdlib/http_body_json_test.fern"), ""},
		// The chunked body decoded as it arrives, against the parser's walk.
		{"http_chunk_decoder", langSrcAbs(t, "tests/stdlib/http_chunk_decoder_test.fern"), ""},
		{"http_response_headers_migrated", langSrcAbs(t, "tests/stdlib/http_response_headers_migrated_test.fern"), ""},
		{"string_prelude_migrated", langSrcAbs(t, "tests/stdlib/string_prelude_migrated_test.fern"), ""},
		{"runner_bench", langSrcAbs(t, "tests/stdlib/runner_bench_test.fern"), "# bench "},
		{"bench_module", langSrcAbs(t, "tests/stdlib/bench_test.fern"), "# Suite: std/bench harness"},
		{"rel_tol_and_ms_bench", langSrcAbs(t, "tests/stdlib/rel_tol_and_ms_bench_test.fern"), "# bench "},
		{"batch8", langSrcAbs(t, "tests/stdlib/batch8_test.fern"), "# golden file bootstrapped at "},
		{"process_assertions", langSrcAbs(t, "tests/stdlib/process_assertions_test.fern"), ""},
		{"process_output_shortcuts", langSrcAbs(t, "tests/stdlib/process_output_shortcuts_test.fern"), ""},
		{"lang_binary_e2e", langSrcAbs(t, "tests/stdlib/lang_binary_e2e_test.fern"), ""},
		{"sort_wider", langSrcAbs(t, "tests/stdlib/sort_wider_test.fern"), ""},
		{"array_reductions", langSrcAbs(t, "tests/stdlib/array_reductions_test.fern"), ""},
		{"array_structural_verbs", langSrcAbs(t, "tests/stdlib/array_structural_verbs_test.fern"), ""},
		{"log", langSrcAbs(t, "tests/stdlib/log_test.fern"), ""},
		{"wide_numerics", langSrcAbs(t, "tests/stdlib/wide_numerics_test.fern"), ""},
		{"wider_array_contains_count", langSrcAbs(t, "tests/stdlib/wider_array_contains_count_test.fern"), ""},
		{"sorted_unique_range", langSrcAbs(t, "tests/stdlib/sorted_unique_range_test.fern"), ""},
		{"all_substring_array", langSrcAbs(t, "tests/stdlib/all_substring_array_test.fern"), ""},
		{"array_prefix_suffix_subseq", langSrcAbs(t, "tests/stdlib/array_prefix_suffix_subseq_test.fern"), ""},
		{"batch7", langSrcAbs(t, "tests/stdlib/batch7_test.fern"), ""},
		{"io_buffered", langSrcAbs(t, "tests/stdlib/io_buffered_test.fern"), ""},
		{"iter", langSrcAbs(t, "tests/stdlib/iter_test.fern"), ""},
		{"ci_string_and_log_kv", langSrcAbs(t, "tests/stdlib/ci_string_and_log_kv_test.fern"), ""},
		{"env_unreachable", langSrcAbs(t, "tests/stdlib/env_unreachable_test.fern"), ""},
		{"file_lines_and_timestamp", langSrcAbs(t, "tests/stdlib/file_lines_and_timestamp_test.fern"), ""},
		{"float", langSrcAbs(t, "tests/stdlib/float_test.fern"), ""},
		{"helpers", langSrcAbs(t, "tests/stdlib/helpers_test.fern"), ""},
		{"json_detail", langSrcAbs(t, "tests/stdlib/json_detail_test.fern"), ""},
		{"one_of_none_of", langSrcAbs(t, "tests/stdlib/one_of_none_of_test.fern"), ""},
		{"set_eq", langSrcAbs(t, "tests/stdlib/set_eq_test.fern"), ""},
		{"string_count_and_dir_listing", langSrcAbs(t, "tests/stdlib/string_count_and_dir_listing_test.fern"), ""},
		{"timing", langSrcAbs(t, "tests/stdlib/timing_test.fern"), ""},
		{"unions_migrated", langSrcAbs(t, "tests/stdlib/unions_migrated_test.fern"), ""},
		{"math", langSrcAbs(t, "tests/stdlib/math_test.fern"), ""},
		{"path", langSrcAbs(t, "tests/stdlib/path_test.fern"), ""},
		{"hex", langSrcAbs(t, "tests/stdlib/hex_test.fern"), ""},
		{"base64", langSrcAbs(t, "tests/stdlib/base64_test.fern"), ""},
		{"url", langSrcAbs(t, "tests/stdlib/url_test.fern"), ""},
		{"deflate", langSrcAbs(t, "tests/stdlib/deflate_test.fern"), ""},
		{"net", langSrcAbs(t, "tests/stdlib/net_test.fern"), ""},
		{"fetch_proxy", langSrcAbs(t, "tests/stdlib/fetch_proxy_test.fern"), ""},
		{"cli", langSrcAbs(t, "tests/stdlib/cli_test.fern"), ""},
		{"format", langSrcAbs(t, "tests/stdlib/format_test.fern"), ""},
		{"csv", langSrcAbs(t, "tests/stdlib/csv_test.fern"), ""},
		{"int", langSrcAbs(t, "tests/stdlib/int_test.fern"), ""},
		{"i32", langSrcAbs(t, "tests/stdlib/i32_test.fern"), ""},
		{"i32_bit_arith", langSrcAbs(t, "tests/stdlib/i32_bit_arith_test.fern"), ""},
		{"i32_to_string_radix", langSrcAbs(t, "tests/stdlib/i32_to_string_radix_test.fern"), ""},
		{"i32_bit_length", langSrcAbs(t, "tests/stdlib/i32_bit_length_test.fern"), ""},
		{"i64", langSrcAbs(t, "tests/stdlib/i64_test.fern"), ""},
		{"i64_range", langSrcAbs(t, "tests/stdlib/i64_range_test.fern"), ""},
		{"i64_bit_ops", langSrcAbs(t, "tests/stdlib/i64_bit_ops_test.fern"), ""},
		{"i64_to_string_radix", langSrcAbs(t, "tests/stdlib/i64_to_string_radix_test.fern"), ""},
		// u64 routes IR now that mono_infer types an `as u64` cast arg, so a
		// bounded-generic assert_eq(x.min(y), v as u64) binds T=u64 (#3457).
		{"u64", langSrcAbs(t, "tests/stdlib/u64_test.fern"), ""},
		{"uuid", langSrcAbs(t, "tests/stdlib/uuid_test.fern"), ""},
		{"json_roundtrip", langSrcAbs(t, "tests/stdlib/json_roundtrip_test.fern"), ""},
		{"json_pointer", langSrcAbs(t, "tests/stdlib/json_pointer_test.fern"), ""},
		{"array_combinators", langSrcAbs(t, "tests/stdlib/array_combinators_test.fern"), ""},
		// Generic array CLOSURE-methods (.reduce / .flat_map / .sort_by / .map[U] /
		// .fold[A]) flipped to IR by the __arrm_* free-generic rewrite (slices 3+4,
		// #3976/#3977); pin the whole 8-test suite on the differential gate so the
		// IR routing can't silently regress behind the synthetic single-function
		// closure/typaram IR tests.
		{"array_hof", langSrcAbs(t, "tests/stdlib/array_hof_test.fern"), ""},
		{"array_accessors", langSrcAbs(t, "tests/stdlib/array_accessors_test.fern"), ""},
		{"array_dedup", langSrcAbs(t, "tests/stdlib/array_dedup_test.fern"), ""},
		{"array_binary_search", langSrcAbs(t, "tests/stdlib/array_binary_search_test.fern"), ""},
		{"array_min_max_index", langSrcAbs(t, "tests/stdlib/array_min_max_index_test.fern"), ""},
		{"array_all_equal", langSrcAbs(t, "tests/stdlib/array_all_equal_test.fern"), ""},
		{"array_none", langSrcAbs(t, "tests/stdlib/array_none_test.fern"), ""},
		{"array_rotate", langSrcAbs(t, "tests/stdlib/array_rotate_test.fern"), ""},
		{"array_batch", langSrcAbs(t, "tests/stdlib/array_batch_test.fern"), ""},
		{"iter_combinators", langSrcAbs(t, "tests/stdlib/iter_combinators_test.fern"), ""},
		{"num_reducers", langSrcAbs(t, "tests/stdlib/num_reducers_test.fern"), ""},
		{"time_iso_span", langSrcAbs(t, "tests/stdlib/time_iso_span_test.fern"), ""},
		{"time_calendar", langSrcAbs(t, "tests/stdlib/time_calendar_test.fern"), ""},
		{"time_http_date", langSrcAbs(t, "tests/stdlib/time_http_date_test.fern"), ""},
		{"tz", langSrcAbs(t, "tests/stdlib/tz_test.fern"), ""},
		{"string_replace_split", langSrcAbs(t, "tests/stdlib/string_replace_split_test.fern"), ""},
		{"string_rsplit_once", langSrcAbs(t, "tests/stdlib/string_rsplit_once_test.fern"), ""},
		{"string_partition", langSrcAbs(t, "tests/stdlib/string_partition_test.fern"), ""},
		{"string_parse_radix", langSrcAbs(t, "tests/stdlib/string_parse_radix_test.fern"), ""},
		{"string_zfill", langSrcAbs(t, "tests/stdlib/string_zfill_test.fern"), ""},
		{"string_swap_case", langSrcAbs(t, "tests/stdlib/string_swap_case_test.fern"), ""},
		{"utf8", langSrcAbs(t, "tests/stdlib/utf8_test.fern"), ""},
		{"string_escape_count", langSrcAbs(t, "tests/stdlib/string_escape_count_test.fern"), ""},
		{"string_slice_extract", langSrcAbs(t, "tests/stdlib/string_slice_extract_test.fern"), ""},
		{"string_classify_transform", langSrcAbs(t, "tests/stdlib/string_classify_transform_test.fern"), ""},
		{"sort_by_and_ci", langSrcAbs(t, "tests/stdlib/sort_by_and_ci_test.fern"), ""},
		{"option_combinators", langSrcAbs(t, "tests/stdlib/option_combinators_test.fern"), ""},
		{"result_combinators", langSrcAbs(t, "tests/stdlib/result_combinators_test.fern"), ""},
		// rc_struct_drop releases a struct's rc-array fields at scope exit (a
		// scalar-array field and a struct-array field) over many alloc→drop
		// cycles; the differential against the interpreter catches a broken
		// drop as a crash or a wrong sum.
		{"rc_struct_drop", langSrcAbs(t, "tests/stdlib/rc_struct_drop_test.fern"), ""},
		// map_verbs flips to IR now that generic map verbs monomorphise: the
		// methods (merge/extend/get_or_insert/entries/…) via the __mapm_ fold
		// (#4016), and the free `from[K,V](pairs: (K,V)[])` via promoting a
		// type-var that feeds a Map[...] position + bind_unify destructuring the
		// `(K,V)[]` tuple arg + mono_infer typing the tuple literal. The last
		// non-async AST-router (#3457).
		{"map_verbs", langSrcAbs(t, "tests/stdlib/map_verbs_test.fern"), ""},
		// crypto + u32 lower fully on the IR path, including remove_dir_all
		// (the TestRunner.finish cleanup call every std/test module reaches).
		// u32 arithmetic must truncate to 32 bits, or SHA-256 miscompiles
		// (#3457).
		{"u32_arith", langSrcAbs(t, "tests/stdlib/u32_arith_test.fern"), ""},
		{"crypto", langSrcAbs(t, "tests/stdlib/crypto_test.fern"), ""},
		{"digest_md5", langSrcAbs(t, "tests/stdlib/digest_md5_test.fern"), ""},
		{"digest_sha1", langSrcAbs(t, "tests/stdlib/digest_sha1_test.fern"), ""},
		{"digest_sha224", langSrcAbs(t, "tests/stdlib/digest_sha224_test.fern"), ""},
		{"digest_sha256", langSrcAbs(t, "tests/stdlib/digest_sha256_test.fern"), ""},
		{"digest_sha384", langSrcAbs(t, "tests/stdlib/digest_sha384_test.fern"), ""},
		{"digest_sha512", langSrcAbs(t, "tests/stdlib/digest_sha512_test.fern"), ""},
		{"digest_blake2b", langSrcAbs(t, "tests/stdlib/digest_blake2b_test.fern"), ""},
		{"digest_sm3", langSrcAbs(t, "tests/stdlib/digest_sm3_test.fern"), ""},
		{"chacha20poly1305", langSrcAbs(t, "tests/stdlib/chacha20poly1305_test.fern"), ""},
		{"aes_gcm", langSrcAbs(t, "tests/stdlib/aes_gcm_test.fern"), ""},
		{"x25519", langSrcAbs(t, "tests/stdlib/x25519_test.fern"), ""},
		{"mlkem768", langSrcAbs(t, "tests/stdlib/mlkem768_test.fern"), ""},
		{"ed25519", langSrcAbs(t, "tests/stdlib/ed25519_test.fern"), ""},
		{"rsa", langSrcAbs(t, "tests/stdlib/rsa_test.fern"), ""},
		{"tls_der", langSrcAbs(t, "tests/stdlib/tls_der_test.fern"), ""},
		{"tls_handshake", langSrcAbs(t, "tests/stdlib/tls_handshake_test.fern"), ""},
		{"tls_keyschedule", langSrcAbs(t, "tests/stdlib/tls_keyschedule_test.fern"), ""},
		{"tls_keyshare", langSrcAbs(t, "tests/stdlib/tls_keyshare_test.fern"), ""},
		{"tls_message", langSrcAbs(t, "tests/stdlib/tls_message_test.fern"), ""},
		{"tls_record", langSrcAbs(t, "tests/stdlib/tls_record_test.fern"), ""},
		{"tls_verify", langSrcAbs(t, "tests/stdlib/tls_verify_test.fern"), ""},
		{"tls_x509", langSrcAbs(t, "tests/stdlib/tls_x509_test.fern"), ""},
		{"hash_checksums", langSrcAbs(t, "tests/stdlib/hash_checksums_test.fern"), ""},
		{"synthetic_fail", failing, ""},
	}
}

// runSelfHostDriverStdin feeds src to a self-host driver binary and returns
// what it wrote to stdout.
func runSelfHostDriverStdin(t *testing.T, runner []string, driverBin, src string, args ...string) []byte {
	t.Helper()
	cmd := runX86_64Bin(runner, driverBin, args...)
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		t.Fatalf("self-host driver failed: %v\n%s", err, out)
	}
	return out
}

// buildBinArm64 assembles+links arm64 asm into dir/name with the aarch64
// gcc toolchain and returns its path. A thin alias for the harness helper,
// which links a HUGE listing (the aarch64 stage-2 self-compile) under a
// memory-budget reservation.
func buildBinArm64(t *testing.T, gcc, dir, name, asm string) string {
	t.Helper()
	return e2eharness.BuildBinArm64(t, gcc, dir, name, asm)
}
