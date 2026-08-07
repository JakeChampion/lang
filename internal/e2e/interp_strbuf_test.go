package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestInterpStrbuf pins the AST interpreter's `strbuf_reset()` /
// `strbuf_append(s)` / `strbuf_take()` builtins — the global string-builder
// primitive (the compiled backends back it with a 64 MiB BSS scratch buffer;
// reset zeroes the length, append adds a string's bytes, take returns the
// accumulated string and resets). Without an interpreter implementation
// (`undefined function "strbuf_reset"`, exit 1) a program using strbuf
// cannot run through the reference oracle even though
// the checker knows the signatures and native / self-host IR implement them.
//
// Each case cross-checks the interpreter exit against the native x86-64 exit
// (every value <= 120). The cases deliberately use a single reset-at-start +
// appends + one take, where the backends agree; a program with a SECOND
// intermediate reset/take diverges only because the native optimizer drops the
// (void-returning) reset/take's side effect — a separate native-codegen issue,
// not an interpreter one.
func TestInterpStrbuf(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"two-appends-len", `function main(): i32 { strbuf_reset(); strbuf_append("hi"); strbuf_append("!"); var s = strbuf_take(); return s.len(); }`, 3},
		{"single-append", `function main(): i32 { strbuf_reset(); strbuf_append("hello"); return strbuf_take().len(); }`, 5},
		{"three-appends-len", `function main(): i32 { strbuf_reset(); strbuf_append("a"); strbuf_append("bc"); strbuf_append("def"); return strbuf_take().len(); }`, 6},
		{"byte-content", `function main(): i32 { strbuf_reset(); strbuf_append("xy"); strbuf_append("z"); var s = strbuf_take(); return (s[0] as i32) + (s[1] as i32) + (s[2] as i32); }`, 107},
		{"empty-take", `function main(): i32 { strbuf_reset(); var s = strbuf_take(); return s.len() + 5; }`, 5},
		{"append-loop", `function main(): i32 { strbuf_reset(); var i = 0; while (i < 10) { strbuf_append("ab"); i = i + 1; } return strbuf_take().len(); }`, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := filepath.Join(t.TempDir(), "sb.fern")
			if err := os.WriteFile(f, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			cmd := exec.Command(interpBin, "-interp", f)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: interp exit %d, want %d", tc.name, code, tc.want)
			}
			if _, code := compileAndRunX86_64(t, tc.src+"\n"); code != tc.want {
				t.Errorf("%s: native x86 exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
