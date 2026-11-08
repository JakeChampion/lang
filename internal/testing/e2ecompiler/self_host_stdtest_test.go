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

func TestSelfHostStdTestE2E(t *testing.T) { runSelfHostStdTestX86(t, false) }

func TestSelfHostStdTestE2ETLS(t *testing.T) { runSelfHostStdTestX86(t, true) }

func runSelfHostStdTestX86(t *testing.T, tls bool) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := selfHostStdTestCases(t, tls)

	t.Run("cases", func(t *testing.T) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				caseDir := t.TempDir()
				wantOut, wantExit := e2eharness.InterpVerdict(t, interpBin, tc.src)
				if wantFail := tc.name == "synthetic_fail"; (wantExit != 0) != wantFail {
					t.Fatalf("interp exit = %d for %s\n%s", wantExit, tc.name, wantOut)
				}

				asm, err := stdTestOutput(runX86_64Bin(runner, mmc, tc.src, stdlibRoot))
				if err != nil {
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

// The suites that take more than a minute under the self-host interpreter. The
// interp leg leaves them out so that it fits one job.
var interpSlowStdTests = map[string]bool{
	// The full CAF corpus includes repeated seeks. Independent trim smoke stays here.
	"caf": true, "caf_shard1": true, "caf_shard2": true, "caf_shard3": true,
	// The CAF boundary campaign takes 89 seconds. Exact trim smoke stays here.
	"caf_boundary": true,
	// The full lossless reference corpus uses compiled gates. Exact ALAC smoke stays here.
	"alac": true,
	// Full Vorbis PCM and transform corpora use compiled gates. Nonzero smoke stays here.
	"vorbis": true, "vorbis_kernel": true, "vorbis_boundary": true,
	"vorbis_file": true, "vorbis_input": true, "vorbis_seek": true,
	// Seek boundaries take 65 seconds. Exact seek smoke stays in the interpreter gate.
	"vorbis_seek_boundary": true,
	// Full MP4 corpora exceed a minute. Independent M4A smoke stays here.
	"mp4": true, "mp4_boundary": true,
	// The full M4A ALAC bank uses compiled gates. Its exact integer smoke stays here.
	"mp4_alac": true,
	// Full AAC corpora exceed a minute. Independent AAC PCM smoke stays here.
	"aac": true, "aac_boundary": true, "aac_kernel": true,
	"brotli": true, "deflate": true, "flac_input": true, "flac_file": true, "ed25519": true, "http_compress": true,
	"http_h2": true, "http_h3q": true, "http_multipart": true, "http_websocket": true,
	"mitigations": true, "mlkem768": true, "mp3": true, "mp3_input": true, "mp3_file": true, "mp3_seek": true, "montgomery": true, "opus": true, "opus_band_analysis": true,
	"opus_bands": true, "opus_celt": true, "opus_celt_encoder": true,
	"opus_celt_frame": true, "opus_celt_prefilter": true, "opus_energy": true,
	// Full PCM corpora use the compiled gates. Independent smoke suites stay here.
	"opus_playback": true, "opus_multistream": true, "opus_file": true, "opus_seek": true, "opus_input": true,
	"opus_mdct": true, "opus_packet": true, "opus_pitch_doubling": true, "opus_pvq": true,
	"opus_rate": true, "opus_vq": true, "ordmap": true, "p256": true, "parse_limits": true,
	"pmap": true, "publicsuffix": true, "pvec": true, "quic_conn": true, "rsa": true, "sim_fetch_h3": true,
	"tls_client": true, "tls_handshake": true, "tls_keyshare": true, "tls_mozilla": true,
	"tls_roots": true, "tls_server": true, "tls_verify": true, "wycheproof_ed25519": true,
	"wycheproof_x25519": true, "x25519": true, "zstd": true,
}

// The self-host interpreter runs each std/test program and must print the same
// TAP stream and exit with the same code as the Go oracle's interpreter.
func TestSelfHostStdTestInterp(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	cli := e2eharness.SelfHostCLI(t)
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	known := map[string]bool{}
	for _, tc := range append(selfHostStdTestCases(t, false), selfHostStdTestCases(t, true)...) {
		known[tc.name] = true
	}
	for name := range interpSlowStdTests {
		if !known[name] {
			t.Errorf("interpSlowStdTests names %q, which is not a std/test case", name)
		}
	}
	for _, tc := range selfHostStdTestCases(t, false) {
		if interpSlowStdTests[tc.name] {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantOut, wantExit := e2eharness.InterpVerdict(t, interpBin, tc.src)
			cmd := exec.Command(cli, "-interp", tc.src, stdlibRoot)
			gotOut, _ := stdTestOutput(cmd)
			if cmd.ProcessState == nil {
				t.Fatalf("the self-host interpreter did not start")
			}
			if gotExit := cmd.ProcessState.ExitCode(); gotExit != wantExit {
				t.Errorf("exit code: self-host interp %d, oracle interp %d\n%s", gotExit, wantExit, gotOut)
			}
			gotStr, wantStr := string(gotOut), string(wantOut)
			if tc.stripPrefix != "" {
				gotStr = stripLinesWithPrefix(gotStr, tc.stripPrefix)
				wantStr = stripLinesWithPrefix(wantStr, tc.stripPrefix)
			}
			if gotStr != wantStr {
				t.Errorf("TAP output mismatch:\n--- self-host interp ---\n%s\n--- oracle interp ---\n%s", gotStr, wantStr)
			}
		})
	}
}

func TestSelfHostStdTestE2EArm64(t *testing.T) { runSelfHostStdTestArm64(t, false) }

func TestSelfHostStdTestE2EArm64TLS(t *testing.T) { runSelfHostStdTestArm64(t, true) }

func runSelfHostStdTestArm64(t *testing.T, tls bool) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "mmc_arm64")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := selfHostStdTestCases(t, tls)
	t.Run("cases", func(t *testing.T) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				caseDir := t.TempDir()
				wantOut, wantExit := e2eharness.InterpVerdict(t, interpBin, tc.src)
				if wantFail := tc.name == "synthetic_fail"; (wantExit != 0) != wantFail {
					t.Fatalf("interp exit = %d for %s\n%s", wantExit, tc.name, wantOut)
				}

				asm, err := stdTestOutput(runX86_64Bin(x86runner, mmc, tc.src, stdlibRoot, "-target", "arm64-linux"))
				if err != nil {
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

// A case peaks under 430 MB in the interpreter and in the compiler
// (quic_conn, x86-64, 2026-11-04). The weight leaves room for twice that and
// lets a four-core runner hold four cases, which the 5 GB weight did not.
func stdTestOutput(cmd *exec.Cmd) ([]byte, error) {
	var out []byte
	err := e2eharness.WithBuildMemoryMB(1024, func() error {
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

func selfHostStdTestCases(t *testing.T, tls bool) []selfHostStdTestCase {
	t.Helper()
	failing := filepath.Join(t.TempDir(), "synthetic_fail_test.fern")
	failSrc := "import \"std/test\";\n" +
		"function bad(): test.TestOutcome { return test.assert_eq(1, 2); }\n" +
		"function main(): i32 {\n" +
		"    let r: test.TestRunner = test.new(\"synthetic\");\n" +
		"    r = r.it(\"one is two\", bad);\n" +
		"    return r.finish();\n" +
		"}\n"
	if err := os.WriteFile(failing, []byte(failSrc), 0o644); err != nil {
		t.Fatalf("write synthetic: %v", err)
	}

	all := []selfHostStdTestCase{
		{"arithmetic", langSrcAbs(t, "tests/stdlib/arithmetic_test.fern"), ""},
		{"strings", langSrcAbs(t, "tests/stdlib/strings_test.fern"), ""},
		{"dns", langSrcAbs(t, "tests/stdlib/dns_test.fern"), ""},
		{"fail_fast", langSrcAbs(t, "tests/stdlib/fail_fast_test.fern"), ""},
		{"quiet_mode", langSrcAbs(t, "tests/stdlib/quiet_mode_test.fern"), ""},
		{"skip_and_subsuites", langSrcAbs(t, "tests/stdlib/skip_and_subsuites_test.fern"), ""},
		{"runner_self", langSrcAbs(t, "tests/stdlib/runner_self_test.fern"), ""},
		{"u8_traits", langSrcAbs(t, "tests/stdlib/u8_traits_test.fern"), ""},
		{"option_and_set_ops", langSrcAbs(t, "tests/stdlib/option_and_set_ops_test.fern"), ""},
		{"result_assertions", langSrcAbs(t, "tests/stdlib/result_assertions_test.fern"), ""},
		{"fuzz_example", langSrcAbs(t, "tests/stdlib/fuzz_example_test.fern"), ""},
		{"fuzz_corpus", langSrcAbs(t, "tests/stdlib/fuzz_corpus_test.fern"), ""},
		{"fuzz_shrink", langSrcAbs(t, "tests/stdlib/fuzz_shrink_test.fern"), ""},
		{"filesystem_ops", langSrcAbs(t, "tests/stdlib/filesystem_ops_test.fern"), ""},
		{"enum_payload_sort_rc", langSrcAbs(t, "tests/stdlib/enum_payload_sort_rc_test.fern"), ""},
		{"ordmap", langSrcAbs(t, "tests/stdlib/ordmap_test.fern"), ""},
		{"ordset", langSrcAbs(t, "tests/stdlib/ordset_test.fern"), ""},
		{"pmap", langSrcAbs(t, "tests/stdlib/pmap_test.fern"), ""},
		{"pset", langSrcAbs(t, "tests/stdlib/pset_test.fern"), ""},
		{"pvec", langSrcAbs(t, "tests/stdlib/pvec_test.fern"), ""},
		{"cmp_traits", langSrcAbs(t, "tests/stdlib/cmp_traits_test.fern"), ""},
		{"collection_vocabulary", langSrcAbs(t, "tests/stdlib/collection_vocabulary_test.fern"), ""},
		{"publicsuffix", langSrcAbs(t, "tests/stdlib/publicsuffix_test.fern"), ""},
		{"float_math", langSrcAbs(t, "tests/stdlib/float_math_test.fern"), ""},
		{"float_convert", langSrcAbs(t, "tests/stdlib/float_convert_test.fern"), ""},
		{"float_hypot", langSrcAbs(t, "tests/stdlib/float_hypot_test.fern"), ""},
		{"float_round_to", langSrcAbs(t, "tests/stdlib/float_round_to_test.fern"), ""},
		{"float_log2_log10", langSrcAbs(t, "tests/stdlib/float_log2_log10_test.fern"), ""},
		{"float_exp2_exp10", langSrcAbs(t, "tests/stdlib/float_exp2_exp10_test.fern"), ""},
		{"float_recip_copysign_midpoint", langSrcAbs(t, "tests/stdlib/float_recip_copysign_midpoint_test.fern"), ""},
		{"i64_roots", langSrcAbs(t, "tests/stdlib/i64_roots_test.fern"), ""},
		{"i64_intdiv", langSrcAbs(t, "tests/stdlib/i64_intdiv_test.fern"), ""},
		{"int_width_parity", langSrcAbs(t, "tests/stdlib/int_width_parity_test.fern"), ""},
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
		{"http_response_bytes", langSrcAbs(t, "tests/stdlib/http_response_bytes_test.fern"), ""},
		{"http_request_builder", langSrcAbs(t, "tests/stdlib/http_request_builder_test.fern"), ""},
		{"http_respond", langSrcAbs(t, "tests/stdlib/http_respond_test.fern"), ""},
		{"http_router", langSrcAbs(t, "tests/stdlib/http_router_test.fern"), ""},
		{"try_inferred_generic", langSrcAbs(t, "tests/stdlib/try_inferred_generic_test.fern"), ""},
		{"http_json_of", langSrcAbs(t, "tests/stdlib/http_json_of_test.fern"), ""},
		{"http_cors", langSrcAbs(t, "tests/stdlib/http_cors_test.fern"), ""},
		{"http_auth", langSrcAbs(t, "tests/stdlib/http_auth_test.fern"), ""},
		{"http_requestid", langSrcAbs(t, "tests/stdlib/http_requestid_test.fern"), ""},
		{"http_hpack", langSrcAbs(t, "tests/stdlib/http_hpack_test.fern"), ""},
		{"http_qpack", langSrcAbs(t, "tests/stdlib/http_qpack_test.fern"), ""},
		{"http_h3", langSrcAbs(t, "tests/stdlib/http_h3_test.fern"), ""},
		{"http_h3q", langSrcAbs(t, "tests/stdlib/http_h3q_test.fern"), ""},
		{"http_h2_frame", langSrcAbs(t, "tests/stdlib/http_h2_frame_test.fern"), ""},
		{"http_h2", langSrcAbs(t, "tests/stdlib/http_h2_test.fern"), ""},
		{"quic_conn", langSrcAbs(t, "tests/stdlib/quic_conn_test.fern"), ""},
		{"quic_frames", langSrcAbs(t, "tests/stdlib/quic_frames_test.fern"), ""},
		{"tcp_conn", langSrcAbs(t, "tests/stdlib/tcp_conn_test.fern"), ""},
		{"quic_packet", langSrcAbs(t, "tests/stdlib/quic_packet_test.fern"), ""},
		{"quic_params", langSrcAbs(t, "tests/stdlib/quic_params_test.fern"), ""},
		{"quic_recovery", langSrcAbs(t, "tests/stdlib/quic_recovery_test.fern"), ""},
		{"quic_streams", langSrcAbs(t, "tests/stdlib/quic_streams_test.fern"), ""},
		{"http_grpc", langSrcAbs(t, "tests/stdlib/http_grpc_test.fern"), ""},
		{"protobuf", langSrcAbs(t, "tests/stdlib/protobuf_test.fern"), ""},
		{"http_cache", langSrcAbs(t, "tests/stdlib/http_cache_test.fern"), ""},
		{"http_static", langSrcAbs(t, "tests/stdlib/http_static_test.fern"), ""},
		{"http_compress", langSrcAbs(t, "tests/stdlib/http_compress_test.fern"), ""},
		{"zstd", langSrcAbs(t, "tests/stdlib/zstd_test.fern"), ""},
		{"brotli", langSrcAbs(t, "tests/stdlib/brotli_test.fern"), ""},
		{"linear_audio", langSrcAbs(t, "tests/stdlib/linear_audio_test.fern"), ""},
		{"linear_audio_boundary", langSrcAbs(t, "tests/stdlib/linear_audio_boundary_test.fern"), ""},
		{"caf_linear", langSrcAbs(t, "tests/stdlib/caf_linear_test.fern"), ""},
		{"caf_linear_boundary", langSrcAbs(t, "tests/stdlib/caf_linear_boundary_test.fern"), ""},
		{"wav_linear", langSrcAbs(t, "tests/stdlib/wav_linear_test.fern"), ""},
		{"wav_linear_boundary", langSrcAbs(t, "tests/stdlib/wav_linear_boundary_test.fern"), ""},
		{"caf", langSrcAbs(t, "tests/stdlib/caf_test.fern"), ""},
		{"caf_shard1", langSrcAbs(t, "tests/stdlib/caf_shard1_test.fern"), ""},
		{"caf_shard2", langSrcAbs(t, "tests/stdlib/caf_shard2_test.fern"), ""},
		{"caf_shard3", langSrcAbs(t, "tests/stdlib/caf_shard3_test.fern"), ""},
		{"caf_boundary", langSrcAbs(t, "tests/stdlib/caf_boundary_test.fern"), ""},
		{"caf_smoke", langSrcAbs(t, "tests/stdlib/caf_smoke_test.fern"), ""},
		{"alac", langSrcAbs(t, "tests/stdlib/alac_test.fern"), ""},
		{"alac_boundary", langSrcAbs(t, "tests/stdlib/alac_boundary_test.fern"), ""},
		{"alac_smoke", langSrcAbs(t, "tests/stdlib/alac_smoke_test.fern"), ""},
		{"vorbis_seek", langSrcAbs(t, "tests/stdlib/vorbis_seek_test.fern"), ""},
		{"vorbis_seek_boundary", langSrcAbs(t, "tests/stdlib/vorbis_seek_boundary_test.fern"), ""},
		{"vorbis_seek_smoke", langSrcAbs(t, "tests/stdlib/vorbis_seek_smoke_test.fern"), ""},
		{"vorbis_file", langSrcAbs(t, "tests/stdlib/vorbis_file_test.fern"), ""},
		{"vorbis_file_boundary", langSrcAbs(t, "tests/stdlib/vorbis_file_boundary_test.fern"), ""},
		{"vorbis_file_smoke", langSrcAbs(t, "tests/stdlib/vorbis_file_smoke_test.fern"), ""},
		{"vorbis_input", langSrcAbs(t, "tests/stdlib/vorbis_input_test.fern"), ""},
		{"vorbis_input_smoke", langSrcAbs(t, "tests/stdlib/vorbis_input_smoke_test.fern"), ""},
		{"vorbis", langSrcAbs(t, "tests/stdlib/vorbis_test.fern"), ""},
		{"vorbis_boundary", langSrcAbs(t, "tests/stdlib/vorbis_boundary_test.fern"), ""},
		{"vorbis_codebook", langSrcAbs(t, "tests/stdlib/vorbis_codebook_test.fern"), ""},
		{"vorbis_kernel", langSrcAbs(t, "tests/stdlib/vorbis_kernel_test.fern"), ""},
		{"vorbis_smoke", langSrcAbs(t, "tests/stdlib/vorbis_smoke_test.fern"), ""},
		{"mp4_alac", langSrcAbs(t, "tests/stdlib/mp4_alac_test.fern"), ""},
		{"mp4_alac_boundary", langSrcAbs(t, "tests/stdlib/mp4_alac_boundary_test.fern"), ""},
		{"mp4_alac_smoke", langSrcAbs(t, "tests/stdlib/mp4_alac_smoke_test.fern"), ""},
		{"mp4", langSrcAbs(t, "tests/stdlib/mp4_test.fern"), ""},
		{"mp4_boundary", langSrcAbs(t, "tests/stdlib/mp4_boundary_test.fern"), ""},
		{"mp4_smoke", langSrcAbs(t, "tests/stdlib/mp4_smoke_test.fern"), ""},
		{"aac", langSrcAbs(t, "tests/stdlib/aac_test.fern"), ""},
		{"aac_boundary", langSrcAbs(t, "tests/stdlib/aac_boundary_test.fern"), ""},
		{"aac_kernel", langSrcAbs(t, "tests/stdlib/aac_kernel_test.fern"), ""},
		{"aac_smoke", langSrcAbs(t, "tests/stdlib/aac_smoke_test.fern"), ""},
		{"audio", langSrcAbs(t, "tests/stdlib/audio_test.fern"), ""},
		{"audio_source", langSrcAbs(t, "tests/stdlib/audio_source_test.fern"), ""},
		{"audio_ogg", langSrcAbs(t, "tests/stdlib/audio_ogg_test.fern"), ""},
		{"video", langSrcAbs(t, "tests/stdlib/video_test.fern"), ""},
		{"video_mp4", langSrcAbs(t, "tests/stdlib/video_mp4_test.fern"), ""},
		{"video_mp4_boundary", langSrcAbs(t, "tests/stdlib/video_mp4_boundary_test.fern"), ""},
		{"android_player", langSrcAbs(t, "tests/stdlib/android_player_test.fern"), ""},
		{"android_video_policy", langSrcAbs(t, "tests/stdlib/android_video_policy_test.fern"), ""},
		{"android_video_source", langSrcAbs(t, "tests/stdlib/android_video_source_test.fern"), ""},
		{"mp3", langSrcAbs(t, "tests/stdlib/mp3_test.fern"), ""},
		{"mp3_file", langSrcAbs(t, "tests/stdlib/mp3_file_test.fern"), ""},
		{"mp3_file_smoke", langSrcAbs(t, "tests/stdlib/mp3_file_smoke_test.fern"), ""},
		{"mp3_input", langSrcAbs(t, "tests/stdlib/mp3_input_test.fern"), ""},
		{"mp3_input_smoke", langSrcAbs(t, "tests/stdlib/mp3_input_smoke_test.fern"), ""},
		{"mp3_seek", langSrcAbs(t, "tests/stdlib/mp3_seek_test.fern"), ""},
		{"mp3_seek_smoke", langSrcAbs(t, "tests/stdlib/mp3_seek_smoke_test.fern"), ""},
		{"flac_file", langSrcAbs(t, "tests/stdlib/flac_file_test.fern"), ""},
		{"flac_file_smoke", langSrcAbs(t, "tests/stdlib/flac_file_smoke_test.fern"), ""},
		{"flac_input", langSrcAbs(t, "tests/stdlib/flac_input_test.fern"), ""},
		{"flac_input_smoke", langSrcAbs(t, "tests/stdlib/flac_input_smoke_test.fern"), ""},
		{"flac_seek", langSrcAbs(t, "tests/stdlib/flac_seek_test.fern"), ""},
		{"opus", langSrcAbs(t, "tests/stdlib/opus_test.fern"), ""},
		{"opus_range", langSrcAbs(t, "tests/stdlib/opus_range_test.fern"), ""},
		{"opus_silk_math", langSrcAbs(t, "tests/stdlib/opus_silk_math_test.fern"), ""},
		{"opus_silk_predict", langSrcAbs(t, "tests/stdlib/opus_silk_predict_test.fern"), ""},
		{"opus_silk_entropy", langSrcAbs(t, "tests/stdlib/opus_silk_entropy_test.fern"), ""},
		{"opus_silk", langSrcAbs(t, "tests/stdlib/opus_silk_test.fern"), ""},
		{"opus_silk_resample", langSrcAbs(t, "tests/stdlib/opus_silk_resample_test.fern"), ""},
		{"opus_silk_output", langSrcAbs(t, "tests/stdlib/opus_silk_output_test.fern"), ""},
		{"opus_silk_loss", langSrcAbs(t, "tests/stdlib/opus_silk_loss_test.fern"), ""},
		{"opus_silk_recovery", langSrcAbs(t, "tests/stdlib/opus_silk_recovery_test.fern"), ""},
		{"opus_silk_fec", langSrcAbs(t, "tests/stdlib/opus_silk_fec_test.fern"), ""},
		{"opus_playback", langSrcAbs(t, "tests/stdlib/opus_playback_test.fern"), ""},
		{"opus_pvq", langSrcAbs(t, "tests/stdlib/opus_pvq_test.fern"), ""},
		{"opus_rate", langSrcAbs(t, "tests/stdlib/opus_rate_test.fern"), ""},
		{"opus_energy", langSrcAbs(t, "tests/stdlib/opus_energy_test.fern"), ""},
		{"opus_vq", langSrcAbs(t, "tests/stdlib/opus_vq_test.fern"), ""},
		{"opus_bands", langSrcAbs(t, "tests/stdlib/opus_bands_test.fern"), ""},
		{"opus_band_analysis", langSrcAbs(t, "tests/stdlib/opus_band_analysis_test.fern"), ""},
		{"opus_mdct", langSrcAbs(t, "tests/stdlib/opus_mdct_test.fern"), ""},
		{"opus_celt", langSrcAbs(t, "tests/stdlib/opus_celt_test.fern"), ""},
		{"opus_celt_encoder", langSrcAbs(t, "tests/stdlib/opus_celt_encoder_test.fern"), ""},
		{"opus_celt_prefilter", langSrcAbs(t, "tests/stdlib/opus_celt_prefilter_test.fern"), ""},
		{"opus_celt_frame", langSrcAbs(t, "tests/stdlib/opus_celt_frame_test.fern"), ""},
		{"opus_packet", langSrcAbs(t, "tests/stdlib/opus_packet_test.fern"), ""},
		{"opus_delimited", langSrcAbs(t, "tests/stdlib/opus_delimited_test.fern"), ""},
		{"opus_headers", langSrcAbs(t, "tests/stdlib/opus_headers_test.fern"), ""},
		{"opus_multistream", langSrcAbs(t, "tests/stdlib/opus_multistream_test.fern"), ""},
		{"opus_multistream_smoke", langSrcAbs(t, "tests/stdlib/opus_multistream_smoke_test.fern"), ""},
		{"opus_file", langSrcAbs(t, "tests/stdlib/opus_file_test.fern"), ""},
		{"opus_file_smoke", langSrcAbs(t, "tests/stdlib/opus_file_smoke_test.fern"), ""},
		{"opus_seek", langSrcAbs(t, "tests/stdlib/opus_seek_test.fern"), ""},
		{"opus_seek_smoke", langSrcAbs(t, "tests/stdlib/opus_seek_smoke_test.fern"), ""},
		{"opus_input", langSrcAbs(t, "tests/stdlib/opus_input_test.fern"), ""},
		{"opus_input_smoke", langSrcAbs(t, "tests/stdlib/opus_input_smoke_test.fern"), ""},
		{"opus_timing", langSrcAbs(t, "tests/stdlib/opus_timing_test.fern"), ""},
		{"opus_pitch", langSrcAbs(t, "tests/stdlib/opus_pitch_test.fern"), ""},
		{"opus_pitch_doubling", langSrcAbs(t, "tests/stdlib/opus_pitch_doubling_test.fern"), ""},
		{"net_broadcast", langSrcAbs(t, "tests/stdlib/net_broadcast_test.fern"), ""},
		{"android_draw", langSrcAbs(t, "tests/stdlib/android_draw_test.fern"), ""},
		{"android_motion", langSrcAbs(t, "tests/stdlib/android_motion_test.fern"), ""},
		{"android_trace", langSrcAbs(t, "tests/stdlib/android_trace_test.fern"), ""},
		{"jpeg", langSrcAbs(t, "tests/stdlib/jpeg_test.fern"), ""},
		{"truetype", langSrcAbs(t, "tests/stdlib/truetype_test.fern"), ""},
		{"android_property", langSrcAbs(t, "tests/stdlib/android_property_test.fern"), ""},
		{"http_forwarded", langSrcAbs(t, "tests/stdlib/http_forwarded_test.fern"), ""},
		{"http_trace", langSrcAbs(t, "tests/stdlib/http_trace_test.fern"), ""},
		{"metrics", langSrcAbs(t, "tests/stdlib/metrics_test.fern"), ""},
		{"http_accesslog", langSrcAbs(t, "tests/stdlib/http_accesslog_test.fern"), ""},
		{"http_cookie", langSrcAbs(t, "tests/stdlib/http_cookie_test.fern"), ""},
		{"http_form", langSrcAbs(t, "tests/stdlib/http_form_test.fern"), ""},
		{"http_multipart", langSrcAbs(t, "tests/stdlib/http_multipart_test.fern"), ""},
		{"http_sse", langSrcAbs(t, "tests/stdlib/http_sse_test.fern"), ""},
		{"http_websocket", langSrcAbs(t, "tests/stdlib/http_websocket_test.fern"), ""},
		{"mock_platform_canned", langSrcAbs(t, "tests/stdlib/mock_platform_canned_test.fern"), ""},
		{"sim_fetch", langSrcAbs(t, "tests/stdlib/sim_fetch_test.fern"), ""},
		{"sim_fetch_h3", langSrcAbs(t, "tests/stdlib/sim_fetch_h3_test.fern"), ""},
		{"sim_platform", langSrcAbs(t, "tests/stdlib/sim_platform_test.fern"), ""},
		{"async_combinators", langSrcAbs(t, "tests/stdlib/async_combinators_test.fern"), ""},
		{"http_body", langSrcAbs(t, "tests/stdlib/http_body_test.fern"), ""},
		{"http_request_body_stream", langSrcAbs(t, "tests/stdlib/http_request_body_stream_test.fern"), ""},
		{"http_body_json", langSrcAbs(t, "tests/stdlib/http_body_json_test.fern"), ""},
		{"http_chunk_decoder", langSrcAbs(t, "tests/stdlib/http_chunk_decoder_test.fern"), ""},
		{"http_response_headers_migrated", langSrcAbs(t, "tests/stdlib/http_response_headers_migrated_test.fern"), ""},
		{"string_prelude_migrated", langSrcAbs(t, "tests/stdlib/string_prelude_migrated_test.fern"), ""},
		{"runner_bench", langSrcAbs(t, "tests/stdlib/runner_bench_test.fern"), "# bench "},
		{"bench_module", langSrcAbs(t, "tests/stdlib/bench_test.fern"), "# Suite: std/bench harness"},
		{"rel_tol_and_ms_bench", langSrcAbs(t, "tests/stdlib/rel_tol_and_ms_bench_test.fern"), "# bench "},
		{"batch8", langSrcAbs(t, "tests/stdlib/batch8_test.fern"), "# golden file bootstrapped at "},
		{"process_assertions", langSrcAbs(t, "tests/stdlib/process_assertions_test.fern"), ""},
		{"process_output_shortcuts", langSrcAbs(t, "tests/stdlib/process_output_shortcuts_test.fern"), ""},
		{"fern_binary_e2e", langSrcAbs(t, "tests/stdlib/fern_binary_e2e_test.fern"), ""},
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
		{"fetch_pool", langSrcAbs(t, "tests/stdlib/fetch_pool_test.fern"), ""},
		{"cli", langSrcAbs(t, "tests/stdlib/cli_test.fern"), ""},
		{"constmap", langSrcAbs(t, "tests/stdlib/constmap_test.fern"), ""},
		{"envflag", langSrcAbs(t, "tests/stdlib/envflag_test.fern"), ""},
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
		{"u64", langSrcAbs(t, "tests/stdlib/u64_test.fern"), ""},
		{"uuid", langSrcAbs(t, "tests/stdlib/uuid_test.fern"), ""},
		{"json_roundtrip", langSrcAbs(t, "tests/stdlib/json_roundtrip_test.fern"), ""},
		{"json_pointer", langSrcAbs(t, "tests/stdlib/json_pointer_test.fern"), ""},
		{"json_object", langSrcAbs(t, "tests/stdlib/json_object_test.fern"), ""},
		{"json_document", langSrcAbs(t, "tests/stdlib/json_document_test.fern"), ""},
		{"regex", langSrcAbs(t, "tests/stdlib/regex_test.fern"), ""},
		{"array_combinators", langSrcAbs(t, "tests/stdlib/array_combinators_test.fern"), ""},
		{"array_hof", langSrcAbs(t, "tests/stdlib/array_hof_test.fern"), ""},
		{"array_accessors", langSrcAbs(t, "tests/stdlib/array_accessors_test.fern"), ""},
		{"array_dedup", langSrcAbs(t, "tests/stdlib/array_dedup_test.fern"), ""},
		{"array_binary_search", langSrcAbs(t, "tests/stdlib/array_binary_search_test.fern"), ""},
		{"array_min_max_index", langSrcAbs(t, "tests/stdlib/array_min_max_index_test.fern"), ""},
		{"array_all_equal", langSrcAbs(t, "tests/stdlib/array_all_equal_test.fern"), ""},
		{"array_none", langSrcAbs(t, "tests/stdlib/array_none_test.fern"), ""},
		{"array_rotate", langSrcAbs(t, "tests/stdlib/array_rotate_test.fern"), ""},
		{"array_count_sum_by", langSrcAbs(t, "tests/stdlib/array_count_sum_by_test.fern"), ""},
		{"array_extremum_by", langSrcAbs(t, "tests/stdlib/array_extremum_by_test.fern"), ""},
		{"array_batch", langSrcAbs(t, "tests/stdlib/array_batch_test.fern"), ""},
		{"iter_combinators", langSrcAbs(t, "tests/stdlib/iter_combinators_test.fern"), ""},
		{"num_reducers", langSrcAbs(t, "tests/stdlib/num_reducers_test.fern"), ""},
		{"time_iso_span", langSrcAbs(t, "tests/stdlib/time_iso_span_test.fern"), ""},
		{"time_calendar", langSrcAbs(t, "tests/stdlib/time_calendar_test.fern"), ""},
		{"time_http_date", langSrcAbs(t, "tests/stdlib/time_http_date_test.fern"), ""},
		{"filetime", langSrcAbs(t, "tests/stdlib/filetime_test.fern"), ""},
		{"fdt", langSrcAbs(t, "tests/stdlib/fdt_test.fern"), ""},
		{"pvh", langSrcAbs(t, "tests/stdlib/pvh_test.fern"), ""},
		{"elf", langSrcAbs(t, "tests/stdlib/elf_test.fern"), ""},
		{"binary", langSrcAbs(t, "tests/stdlib/binary_test.fern"), ""},
		{"tz", langSrcAbs(t, "tests/stdlib/tz_test.fern"), ""},
		{"string_replace_split", langSrcAbs(t, "tests/stdlib/string_replace_split_test.fern"), ""},
		{"string_rsplit_once", langSrcAbs(t, "tests/stdlib/string_rsplit_once_test.fern"), ""},
		{"string_partition", langSrcAbs(t, "tests/stdlib/string_partition_test.fern"), ""},
		{"string_parse_radix", langSrcAbs(t, "tests/stdlib/string_parse_radix_test.fern"), ""},
		{"string_parse_bool", langSrcAbs(t, "tests/stdlib/string_parse_bool_test.fern"), ""},
		{"string_zfill", langSrcAbs(t, "tests/stdlib/string_zfill_test.fern"), ""},
		{"string_swap_case", langSrcAbs(t, "tests/stdlib/string_swap_case_test.fern"), ""},
		{"utf8", langSrcAbs(t, "tests/stdlib/utf8_test.fern"), ""},
		{"string_escape_count", langSrcAbs(t, "tests/stdlib/string_escape_count_test.fern"), ""},
		{"string_slice_extract", langSrcAbs(t, "tests/stdlib/string_slice_extract_test.fern"), ""},
		{"string_classify_transform", langSrcAbs(t, "tests/stdlib/string_classify_transform_test.fern"), ""},
		{"sort_by_and_ci", langSrcAbs(t, "tests/stdlib/sort_by_and_ci_test.fern"), ""},
		{"sort_family", langSrcAbs(t, "tests/stdlib/sort_family_test.fern"), ""},
		{"option_combinators", langSrcAbs(t, "tests/stdlib/option_combinators_test.fern"), ""},
		{"result_combinators", langSrcAbs(t, "tests/stdlib/result_combinators_test.fern"), ""},
		{"rc_struct_drop", langSrcAbs(t, "tests/stdlib/rc_struct_drop_test.fern"), ""},
		{"map_verbs", langSrcAbs(t, "tests/stdlib/map_verbs_test.fern"), ""},
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
		{"field25519", langSrcAbs(t, "tests/stdlib/field25519_test.fern"), ""},
		{"x25519", langSrcAbs(t, "tests/stdlib/x25519_test.fern"), ""},
		{"mlkem768", langSrcAbs(t, "tests/stdlib/mlkem768_test.fern"), ""},
		{"montgomery", langSrcAbs(t, "tests/stdlib/montgomery_test.fern"), ""},
		{"p256", langSrcAbs(t, "tests/stdlib/p256_test.fern"), ""},
		{"ed25519", langSrcAbs(t, "tests/stdlib/ed25519_test.fern"), ""},
		{"wycheproof_aes_gcm", langSrcAbs(t, "tests/stdlib/wycheproof_aes_gcm_test.fern"), ""},
		{"wycheproof_chacha20poly1305", langSrcAbs(t, "tests/stdlib/wycheproof_chacha20poly1305_test.fern"), ""},
		{"wycheproof_x25519", langSrcAbs(t, "tests/stdlib/wycheproof_x25519_test.fern"), ""},
		{"wycheproof_ed25519", langSrcAbs(t, "tests/stdlib/wycheproof_ed25519_test.fern"), ""},
		{"rsa", langSrcAbs(t, "tests/stdlib/rsa_test.fern"), ""},
		{"tls_client", langSrcAbs(t, "tests/stdlib/tls_client_test.fern"), ""},
		{"tls_der", langSrcAbs(t, "tests/stdlib/tls_der_test.fern"), ""},
		{"tls_handshake", langSrcAbs(t, "tests/stdlib/tls_handshake_test.fern"), ""},
		{"tls_keyschedule", langSrcAbs(t, "tests/stdlib/tls_keyschedule_test.fern"), ""},
		{"tls_keyshare", langSrcAbs(t, "tests/stdlib/tls_keyshare_test.fern"), ""},
		{"tls_message", langSrcAbs(t, "tests/stdlib/tls_message_test.fern"), ""},
		{"tls_mozilla", langSrcAbs(t, "tests/stdlib/tls_mozilla_test.fern"), ""},
		{"tls_record", langSrcAbs(t, "tests/stdlib/tls_record_test.fern"), ""},
		{"tls_roots", langSrcAbs(t, "tests/stdlib/tls_roots_test.fern"), ""},
		{"tls_server", langSrcAbs(t, "tests/stdlib/tls_server_test.fern"), ""},
		{"tls_tls12", langSrcAbs(t, "tests/stdlib/tls12_test.fern"), ""},
		{"tls_verify", langSrcAbs(t, "tests/stdlib/tls_verify_test.fern"), ""},
		{"tls_x509", langSrcAbs(t, "tests/stdlib/tls_x509_test.fern"), ""},
		{"hash_checksums", langSrcAbs(t, "tests/stdlib/hash_checksums_test.fern"), ""},
		{"mitigations", langSrcAbs(t, "tests/stdlib/mitigations_test.fern"), ""},
		{"parse_limits", langSrcAbs(t, "tests/stdlib/parse_limits_test.fern"), ""},
		{"worker", langSrcAbs(t, "tests/stdlib/worker_test.fern"), ""},
		{"sim_worker", langSrcAbs(t, "tests/stdlib/sim_worker_test.fern"), ""},
		{"synthetic_fail", failing, ""},
	}
	var cases []selfHostStdTestCase
	for _, tc := range all {
		if strings.HasPrefix(tc.name, "tls_") == tls {
			cases = append(cases, tc)
		}
	}
	return cases
}

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

func buildBinArm64(t *testing.T, gcc, dir, name, asm string) string {
	t.Helper()
	return e2eharness.BuildBinArm64(t, gcc, dir, name, asm)
}
