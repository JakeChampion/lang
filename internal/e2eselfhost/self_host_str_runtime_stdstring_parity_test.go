package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// A `.split(sep)` / `.lines()` / `.trim()` written against std/string does NOT
// reach std/string under the self-host: irlower lowers each to a runtime helper
// the compiler emits itself (asmcore.rt_src_str_split / _lines / _trim). Those
// helpers therefore ARE std/string as far as a self-hosted program is concerned,
// and every place they disagreed with it was one program with two answers,
// silently — nothing compared them.
//
// Three disagreements were live, all of them "what is a string made of":
//
//	split("")  std/string steps by CODEPOINT (#8469); the helper stepped by BYTE,
//	           so "héllo".split("") was 6 fragments to std/string's 5 characters
//	           and h[1] was half of "é".
//	lines()    std/string strips a trailing '\r' so CRLF reads as LF; the helper
//	           kept it, so every line of a CRLF file ended in a stray byte.
//	trim()     std/string's __is_ascii_ws is six bytes (32/9/10/13/11/12); the
//	           helper knew four and left '\v' / '\f' in place.
//
// Each case is compared against the native interpreter, which runs std/string
// itself — so the oracle is the definition these helpers must transcribe.
//
// NO WASM LEG: the wasm backend does not use these helpers at all. It carries
// its own hand-written str_split_helper / str_lines_helper / str_trim_helper in
// examples/self_host/wasm_ir.fern, and all three still disagree with std/string
// exactly as the register-backend helpers did. That is unfixed.
var strRuntimeStdStringParityCases = []struct {
	name string
	src  string
}{
	// split(""), the codepoint unit.
	{"split_empty_sep_ascii", `import "std/string";
function main(): i32 { var p: string[] = "abc".split(""); return p.len() * 10 + p[0].len(); }`},
	{"split_empty_sep_two_byte", `import "std/string";
function main(): i32 { var p: string[] = "héllo".split(""); return p.len() * 10 + p[1].len(); }`},
	{"split_empty_sep_astral", `import "std/string";
function main(): i32 { var p: string[] = "a😀b".split(""); return p.len() * 10 + p[1].len(); }`},
	// The pieces must be the right STRINGS, not merely the right count: a byte
	// split gets p.len() wrong AND every piece wrong.
	{"split_empty_sep_piece_values", `import "std/string";
function main(): i32 {
    var p: string[] = "héllo".split("");
    var r: i32 = 0;
    if (p[0] == "h") { r = r + 1; }
    if (p[1] == "é") { r = r + 2; }
    if (p[2] == "l") { r = r + 4; }
    if (p[3] == "l") { r = r + 8; }
    if (p[4] == "o") { r = r + 16; }
    return r;
}`},
	// Ill-formed UTF-8: the step consumes one MAXIMAL SUBPART per piece, so
	// `F1 80 62` is two pieces (not three, not one) and a lone truncated lead is
	// one. This is the half of __utf8_step a well-formed input never exercises.
	{"split_empty_sep_ill_formed", `import "std/string";
function main(): i32 {
    var trunc: string = string_from_bytes_unchecked([0xF1 as u8, 0x80 as u8, 0x62 as u8]);
    var short: string = string_from_bytes_unchecked([0xE2 as u8, 0x82 as u8]);
    var overlong: string = string_from_bytes_unchecked([0xC0 as u8, 0xAF as u8]);
    var surrogate: string = string_from_bytes_unchecked([0xED as u8, 0xA0 as u8, 0x80 as u8]);
    return trunc.split("").len() * 27 + short.split("").len() * 9
        + overlong.split("").len() * 3 + surrogate.split("").len();
}`},
	{"split_empty_sep_empty_input", `import "std/string";
function main(): i32 { return "".split("").len() + 7; }`},
	// Control: a non-empty separator is byte-exact in both, and must stay so.
	{"split_non_empty_sep", `import "std/string";
function main(): i32 {
    var a: string[] = "a,b,".split(",");
    var b: string[] = ",a,b".split(",");
    var c: string[] = "axxbxxc".split("xx");
    return a.len() * 10 + b[0].len() + c.len();
}`},
	// lines(): CR stripping.
	{"lines_crlf", `import "std/string";
function main(): i32 {
    var l: string[] = "a\r\nbb\r\n".lines();
    return l.len() * 100 + l[0].len() * 10 + l[1].len();
}`},
	{"lines_bare_cr_tail", `import "std/string";
function main(): i32 { var l: string[] = "a\r".lines(); return l.len() * 10 + l[0].len(); }`},
	{"lines_edges", `import "std/string";
function main(): i32 {
    return "".lines().len() * 100 + "\n".lines().len() * 10 + "x\ny\n".lines().len();
}`},
	// trim(): the whitespace set.
	{"trim_vertical_tab_and_form_feed", `import "std/string";
function main(): i32 {
    var s: string = string_from_bytes_unchecked([32 as u8, 9 as u8, 11 as u8, 12 as u8,
        104 as u8, 105 as u8, 13 as u8, 10 as u8, 32 as u8]);
    return s.trim().len() + 40;
}`},
	{"trim_all_whitespace", `import "std/string";
function main(): i32 {
    var s: string = string_from_bytes_unchecked([11 as u8, 12 as u8]);
    return s.trim().len() + 40;
}`},
	// Controls for the neighbouring helpers, which already agreed: the ASCII case
	// folds leave a multibyte sequence alone, and byte-offset search is byte-exact
	// on both sides.
	{"ascii_case_fold_leaves_non_ascii", `import "std/string";
function main(): i32 {
    var u: string = "héllo".to_ascii_upper();
    var r: i32 = 0;
    if (u == "HéLLO") { r = 1; }
    if ("HÉLLO".to_ascii_lower() == "hÉllo") { r = r + 2; }
    return u.len() * 10 + r;
}`},
	{"search_offsets_are_bytes", `import "std/string";
function main(): i32 {
    return "héllo".index_of("é") * 100 + "héllo".index_of("llo") * 10 + "abc".index_of("") + 1;
}`},
}

// TestSelfHostStrRuntimeStdStringParityX86_64 and its arm64 sibling both compile
// with the production self-hosted driver and compare against the interpreter.
func TestSelfHostStrRuntimeStdStringParityX86_64(t *testing.T) {
	fernBin, stdlibRoot, runner := selfHostProdDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range strRuntimeStdStringParityCases {
		t.Run(tc.name, func(t *testing.T) {
			want := selfHostInterpOracle(t, interpBin, tc.src)
			mainPath := writeSelfHostProgram(t, tc.src)
			binPath := filepath.Join(filepath.Dir(mainPath), "out.bin")
			if out, err := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", mainPath, stdlibRoot, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(binPath)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("program did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s (x86-64) = %d, want %d (interp oracle running std/string itself)", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostStrRuntimeStdStringParityArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	fernBin, stdlibRoot, runner := selfHostProdDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range strRuntimeStdStringParityCases {
		t.Run(tc.name, func(t *testing.T) {
			want := selfHostInterpOracle(t, interpBin, tc.src)
			mainPath := writeSelfHostProgram(t, tc.src)
			binPath := filepath.Join(filepath.Dir(mainPath), "out.bin")
			if out, err := runX86_64Bin(runner, fernBin, "-target", "arm64-linux", mainPath, stdlibRoot, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			cmd := runArm64Bin(qemu, binPath)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("program did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s (arm64) = %d, want %d (interp oracle running std/string itself)", tc.name, got, want)
			}
		})
	}
}
