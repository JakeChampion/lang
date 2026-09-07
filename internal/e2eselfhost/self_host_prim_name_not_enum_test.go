package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// #8428: `is_enum_like_name` excluded i32 / boolean / bool / string / i64 / f64 /
// f32 / float / void / clo and then said YES to every other bracket-free name
// that was not a struct or an array — so `u8`, `u32`, `u64`, `usize`, `char` and
// `str` all came back "a bare nominal enum". The predicate now excludes the
// whole primitive set (is_prim_type_name), and the gates that had been leaning
// on the over-match name the primitive spellings they actually handle.
//
// The shapes here are the ones the conformance corpus does not carry. What the
// corpus does cover, and so is not repeated: `int_checked_div` /
// `match_expr_arm_width` (u32 / u64 Option payloads), `string_slice_option`
// (a `str` payload), `char_cast_receiver` (a `var c: char` receiver) and the
// whole `core/bigint` `(u64[], u64)` return chain — every one of them reached
// the IR path only through the over-match, and they are gated by
// `TestFernFixturesSelfHostX86_64` under FERN_SELFHOST_FIXTURES=1.
var primNameNotEnumCases = []struct {
	name string
	src  string
}{
	// A `(u64[], u64)` tuple return, read back through `.N[i]` AND through a
	// destructure. Both were admitted as an array-of-ENUM element, which marks
	// the bound slot mark_arr + mark_struct_type("u64") — no 8-byte element
	// kind — so the destructured array's `a[1]` read its low 32 bits. This
	// case exits 4 on the compiler before the fix and 0 after; the tuple
	// element is now admitted as the leak-safe 8-byte array it is
	// (mark_i64arr + mark_u64, like an annotated `var xs: u64[]`).
	{"u64-array-tuple-element", "function mk(): (u64[], u64) {\n" +
		"    var q: u64[] = [];\n    q = q.append(18446744073709551615 as u64);\n    q = q.append(4294967296 as u64);\n" +
		"    return (q, 4294967297 as u64);\n}\n" +
		"function main(): i32 {\n" +
		"    var t: (u64[], u64) = mk();\n" +
		"    if (t.0[1] != (4294967296 as u64)) { return 1; }\n" +
		"    if (t.0[0] != (18446744073709551615 as u64)) { return 2; }\n" +
		"    if (t.1 != (4294967297 as u64)) { return 3; }\n" +
		"    var (a, b) = mk();\n" +
		"    if (a[1] != (4294967296 as u64)) { return 4; }\n" +
		"    if (b != (4294967297 as u64)) { return 5; }\n" +
		"    var xs: u64[] = t.0;\n" +
		"    if (xs[1] != (4294967296 as u64)) { return 6; }\n" +
		"    return 0;\n}"},
	// A `u64[]` ENUM PAYLOAD. is_enum_array_field_type said "array of enum",
	// which put the payload on the box-walking drop path: `__fern_arrarr_free`
	// dereferenced each u64 limb as an rc box. 18446744073709551615 is not a
	// plausible address, so this SIGSEGVs before the fix — the lucky outcome,
	// since a limb holding one would have decremented whatever it pointed at
	// instead. The arm reads only `.len()`: the crash is on the DROP, and a
	// wide element read off an enum payload needs the checker's type
	// annotations, which the stdin driver does not run.
	{"u64-array-enum-payload", "enum E { Xs(u64[]), N }\n" +
		"function main(): i32 {\n" +
		"    var e: E = Xs([18446744073709551615 as u64, 5 as u64]);\n" +
		"    match (e) { Xs(v) => { return v.len() - 2; }, N => { return 3; } }\n" +
		"    return 9;\n}"},
	// The plain `struct Q { us: u64[] }` construction from the issue. It is
	// leak-safe on its own terms (is_leaksafe_array_field covers u64[]), which
	// is tested before the enum-array arm — so this one is a pin that the
	// narrowing did not disturb it, not a repro.
	{"u64-array-struct-field", "struct Q { us: u64[] }\n" +
		"function main(): i32 {\n" +
		"    var q: Q = Q { us: [18446744073709551615 as u64, 2 as u64] };\n" +
		"    if (q.us[0] != (18446744073709551615 as u64)) { return 1; }\n" +
		"    if (q.us[1] != (2 as u64)) { return 2; }\n" +
		"    return 0;\n}"},
	// `char[]` as a struct field. It too was admitted as an array-of-enum and
	// dropped by a box walk; it is a flat 4-byte-slot scalar buffer, so it now
	// rides is_leaksafe_array_field with i32[] / u32[] / u8[].
	{"char-array-struct-field", "struct C { cs: char[] }\n" +
		"function main(): i32 {\n" +
		"    var c: C = C { cs: [97 as char, 98 as char] };\n" +
		"    if ((c.cs[0] as i32) != 97) { return 1; }\n" +
		"    if ((c.cs[1] as i32) != 98) { return 2; }\n" +
		"    return 0;\n}"},
}

// TestSelfHostPrimNameNotEnumX86_64 — the production x86-64 IR path, against the
// interpreter oracle, with FERN_STRICT_IR=1 so a case cannot pass by bailing.
func TestSelfHostPrimNameNotEnumX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range primNameNotEnumCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			progBin := buildBin(t, gcc, dir, "pnne_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally (signal): %v", tc.name, cmd.ProcessState)
			}
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostUsizeArrayFieldRefused pins the one admission the narrowing turns
// off. A `usize[]` struct field used to be admitted as an array-of-enum, which
// put a box walk over 8-byte integers on the drop path — the same silent heap
// corruption the u64[] payload above faults on. usize[] is NOT leak-safe-array
// classified: unlike u64[] it has no 8-byte field-tag plumbing, so admitting it
// as a 4-byte-element array would read half of every element. Refusing it, with
// a bail site the diagnostic names, is the correct answer until that plumbing
// lands; this test fails the day it does, which is when the row moves.
func TestSelfHostUsizeArrayFieldRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = "struct U { ps: usize[] }\n" +
		"function main(): i32 {\n    var u: U = U { ps: [1 as usize, 2 as usize] };\n    return u.ps.len() - 2;\n}"

	cmd := exec.Command(driverBin, "-ir")
	if len(runner) > 0 {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin, "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &bytes.Buffer{}
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("driver did not exit normally: %v", cmd.ProcessState)
	}
	if code := cmd.ProcessState.ExitCode(); code != 3 {
		t.Fatalf("usize[] field: driver exited %d under FERN_STRICT_IR=1, want 3 (a named bail)\n%s", code, stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got, "FERN_STRICT_IR:") || !strings.Contains(got, "struct literal `U`") {
		t.Errorf("bail diagnostic does not name the site:\n%s", got)
	}
}
