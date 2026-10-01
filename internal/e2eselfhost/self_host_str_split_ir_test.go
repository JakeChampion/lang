package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// strSplitIRCases are string.split(sep) and the other string-transform method
// programs the typed lowering must produce (the path probe prints "ir"). This
// is the verdict that pairs with the differential cases in
// TestSelfHostAsmIRPath (split-*), which prove the chosen path computes the
// right answer.
var strSplitIRCases = []struct {
	name string
	src  string
}{
	{"split-method-bind", `function main(): i32 { var p = "a,b,c".split(","); return p.len(); }`},
	{"split-index", `function main(): i32 { var p = "foo,bar".split(","); return p[1].len(); }`},
	{"split-multichar", `function main(): i32 { var p = "axxb".split("xx"); return p.len(); }`},
	{"split-empty-sep", `function main(): i32 { var p = "abc".split(""); return p.len(); }`},
	{"split-loop", `function main(): i32 { var p = "a,bb,ccc".split(","); var s = 0; var i = 0; while (i < p.len()) { s = s + p[i].len(); i = i + 1; } return s; }`},
	{"split-forin", `function main(): i32 { var s = 0; for part in "x,yy".split(",") { s = s + part.len(); } return s; }`},
	{"split-param", `function nf(s: string): i32 { return s.split(",").len(); } function main(): i32 { return nf("a,b,c"); }`},
	{"split-direct-index", `function main(): i32 { return "one,two,three".split(",")[2].len(); }`},
	// ASCII case transforms (op_str_to_upper / _to_lower) — likewise IR-eligible.
	{"to-upper", `function main(): i32 { return "Hello".to_ascii_upper().len(); }`},
	{"to-lower", `function main(): i32 { var s = "ABC"; return s.to_ascii_lower()[0] as i32; }`},
	{"case-roundtrip", `function main(): i32 { if ("Hi".to_ascii_upper().to_ascii_lower() == "hi") { return 1; } return 0; }`},
	{"case-param", `function up(s: string): i32 { return s.to_ascii_upper()[0] as i32; } function main(): i32 { return up("xyz"); }`},
	// String repeat (op_str_repeat) — likewise IR-eligible.
	{"repeat", `function main(): i32 { return "ab".repeat(3).len(); }`},
	{"repeat-var", `function main(): i32 { var s = "x"; var n = 4; return s.repeat(n).len(); }`},
	// String trim (op_str_trim) — likewise IR-eligible.
	{"trim", `function main(): i32 { return "  hi  ".trim().len(); }`},
	{"trim-param", `function tn(s: string): i32 { return s.trim().len(); } function main(): i32 { return tn("  x  "); }`},
	// String replace (op_str_replace) -- likewise IR-eligible.
	{"replace", `function main(): i32 { return "a-b-c".replace("-", "_").len(); }`},
	{"replace-param", `function rp(s: string): i32 { return s.replace("o", "0").len(); } function main(): i32 { return rp("foo"); }`},
	// String lines (op_str_lines) -- likewise IR-eligible.
	{"lines", `function main(): i32 { return "a\nb\nc".lines().len(); }`},
	{"lines-forin", `function main(): i32 { var n = 0; for ln in "x\ny".lines() { n = n + 1; } return n; }`},
}

// TestSelfHostStrSplitIRPathX86_64 asserts each split program routes through the
// "ir" path via the asm_pathprobe_run driver (semlower.verdict, the same probe
// the trait IR-path test uses; it prints "ir"/"refused" and emits nothing).
func TestSelfHostStrSplitIRPathX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_pathprobe_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range strSplitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			out := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if out != "ir" {
				t.Errorf("%s routed through %q path, want \"ir\"", tc.name, out)
			}
		})
	}
}
