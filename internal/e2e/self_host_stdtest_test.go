package e2e

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
	for _, name := range []string{"flatten.fern", "asm_load_run.fern"} {
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
		"function bad(): Option[string] { return test.assert_eq_i32(1, 2); }\n" +
		"function main(): i32 {\n" +
		"    var r: test.TestRunner = test.test_new(\"synthetic\");\n" +
		"    r = r.it(\"one is two\", bad());\n" +
		"    return r.finish();\n" +
		"}\n"
	if err := os.WriteFile(failing, []byte(failSrc), 0o644); err != nil {
		t.Fatalf("write synthetic: %v", err)
	}

	cases := []struct {
		name string
		src  string
		// stripPrefix lets a case ignore output lines that start with
		// this prefix on both sides — used for `# bench …` timing
		// comments whose digits differ run-to-run.
		stripPrefix string
	}{
		{"arithmetic", langSrcAbs(t, "examples/tests/arithmetic_test.fern"), ""},
		{"strings", langSrcAbs(t, "examples/tests/strings_test.fern"), ""},
		{"fail_fast", langSrcAbs(t, "examples/tests/fail_fast_test.fern"), ""},
		{"quiet_mode", langSrcAbs(t, "examples/tests/quiet_mode_test.fern"), ""},
		{"skip_and_subsuites", langSrcAbs(t, "examples/tests/skip_and_subsuites_test.fern"), ""},
		// `runner_self_test` inspects the runner's own failure
		// messages — passes only once match arms on
		// Option[string] locals / call results bind the payload
		// as `string` (so msg.contains(...) routes through the
		// string method, not shape dispatch).
		{"runner_self", langSrcAbs(t, "examples/tests/runner_self_test.fern"), ""},
		// `option_and_set_ops` matches on `test.assert_is_some_i32(...)`
		// (a call returning Option[string]); same payload-typing path
		// drives msg.contains and the array-set assertions.
		{"option_and_set_ops", langSrcAbs(t, "examples/tests/option_and_set_ops_test.fern"), ""},
		// `result_assertions` walks the Ok payload of a
		// Result[string[], IoError] (`v.len()` inside an Ok arm). Passes
		// once ret_tag_of preserves the Result Ok type instead of
		// collapsing it to "result:unknown".
		{"result_assertions", langSrcAbs(t, "examples/tests/result_assertions_test.fern"), ""},
		// std/fuzz suites — each imports std/fuzz + std/test and uses
		// `Ok(...)` / `Err(...)` constructors inside Lang bodies. Pass
		// once the self-host lowers those as Result heap boxes rather
		// than emitting `call __fn_Ok` / `call __fn_Err`.
		{"fuzz_example", langSrcAbs(t, "examples/tests/fuzz_example_test.fern"), ""},
		{"fuzz_corpus", langSrcAbs(t, "examples/tests/fuzz_corpus_test.fern"), ""},
		{"fuzz_shrink", langSrcAbs(t, "examples/tests/fuzz_shrink_test.fern"), ""},
		// `filesystem_ops` exercises remove_file alongside temp_dir /
		// write_file / stat / read_dir / remove_dir_all. Joins the gate
		// once the self-host has the remove_file builtin (unlinkat
		// helper, Option[IoError] result).
		{"filesystem_ops", langSrcAbs(t, "examples/tests/filesystem_ops_test.fern"), ""},
		// `float_math` / `float_array_strict_sort` — both need IEEE
		// NaN semantics for f64 compares (every relation with NaN is
		// false except `!=`). Pass once x86 masks the affected
		// comparisons with setnp / setp.
		{"float_math", langSrcAbs(t, "examples/tests/float_math_test.fern"), ""},
		{"float_array_strict_sort", langSrcAbs(t, "examples/tests/float_array_strict_sort_test.fern"), ""},
		// `lines_log` — needs `.lines()` to NOT produce a phantom empty
		// trailing line when input ends with `\n` (matches the interp +
		// Rust/Python). Passes once the self-host's lines() emit
		// decrements the array len in the empty / trailing-`\n` case.
		{"lines_log", langSrcAbs(t, "examples/tests/lines_log_test.fern"), ""},
		// `assert_at_wider` / `array_at_and_f32_range` — need `u32` /
		// `u64` / `i64` type tags to route through the i32 codegen
		// path so `(n as u32).to_string()` doesn't segfault.
		{"assert_at_wider", langSrcAbs(t, "examples/tests/assert_at_wider_test.fern"), ""},
		{"array_at_and_f32_range", langSrcAbs(t, "examples/tests/array_at_and_f32_range_test.fern"), ""},
		// `map_eq_and_predicates` — needs `m.get_or(k, default)` to
		// be dispatched (inline Some-or-default on __fern_map_get)
		// instead of falling through to core/map's pure-Lang body.
		{"map_eq_and_predicates", langSrcAbs(t, "examples/tests/map_eq_and_predicates_test.fern"), ""},
		// `json_field_eq` — exercises every JsonValue arm, including
		// `JNumber(s) => s.parse_int()` whose Some(got) payload binding
		// was untyped before match_payload_type's ExprFieldAccess
		// branch learned to look up receiver methods.
		{"json_field_eq", langSrcAbs(t, "examples/tests/json_field_eq_test.fern"), ""},
		// `header_map_migrated` / `http_request_headers_migrated` —
		// pass once std/headers + std/http declare their HeaderMap /
		// HttpRequest structs explicitly (the Go checker inferred them
		// from constructor literals; the self-host's shape-pointer
		// dispatch needs the decl to resolve field offsets).
		{"header_map_migrated", langSrcAbs(t, "examples/tests/header_map_migrated_test.fern"), ""},
		{"http_request_headers_migrated", langSrcAbs(t, "examples/tests/http_request_headers_migrated_test.fern"), ""},
		// `http_response_headers_migrated` — needs `int.int_to_string`
		// (the qualified call modload mangles to `int__int_to_string`)
		// to route to __fern_i32_to_string. Without it, core/int's
		// pure-Fern body runs through raw __memcpy / u8[] machinery
		// and produces empty strings, so the serialized response had
		// "HTTP/1.1  OK" (status missing) and "Content-Length: ".
		{"http_response_headers_migrated", langSrcAbs(t, "examples/tests/http_response_headers_migrated_test.fern"), ""},
		// `string_prelude_migrated` — uses `var ab: [u8]` (the prefix-
		// bracket type syntax). Before, the self-host's parse_type_name
		// fell through with the `[` left on the cursor and the
		// surrounding decl loop spun until the kernel OOM-killed the
		// compiler. Now `[T]` desugars to `T[]` so it goes through the
		// existing postfix-array path.
		{"string_prelude_migrated", langSrcAbs(t, "examples/tests/string_prelude_migrated_test.fern"), ""},
		// `bench_test` / `rel_tol_and_ms_bench_test` — pass once
		// method-call `fn`-typed args box bare function idents (so
		// `r.bench("…", n, my_workload)` doesn't call my_workload
		// 0-arg in the caller). Their `# bench …` comment lines
		// include per-iteration timings that differ run-to-run, so
		// the gate strips those before comparing.
		{"bench", langSrcAbs(t, "examples/tests/bench_test.fern"), "# bench "},
		{"rel_tol_and_ms_bench", langSrcAbs(t, "examples/tests/rel_tol_and_ms_bench_test.fern"), "# bench "},
		{"synthetic_fail", failing, ""},
	}

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
