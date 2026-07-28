package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArm64StrBufTake guards a fix to the arm64 backend's
// `returnIsString` table: `strbuf_take` returns a string-typed
// value under the two-word ABI, so its OpCallDirect emit must
// push BOTH x0 (data ptr) and x1 (byte length). Before, the
// table only listed `random_bytes` / `tcp_recv` /
// `string_from_bytes_unchecked` / `__str_slice` — `strbuf_take` went
// through the single-word path, the second-word store loaded
// garbage from the stack as the string length, and any program
// using strbuf (notably the asm self-host itself) silently
// produced wrong output: a 0x10000000-byte write to fd 1 that
// EFAULTs, then the trailing newline from `print`.
//
// The most visible symptom was the arm64 self-host
// (`asm_load_run.fern`, whose -target flag also covers arm64) compiling cleanly through the Go
// arm64 backend but emitting 0 bytes of asm at runtime — the
// strbuf-take-then-print chain at the end of `emit_module`
// silently dropped its payload.
//
// This test pins the smallest reproducer (3 chars, one append,
// one take) so a regression to that path fails fast and points
// at strbuf — not at the bigger self-host driver.
func TestArm64StrBufTake(t *testing.T) {
	src := "function main(): i32 {\n" +
		"    strbuf_reset();\n" +
		"    strbuf_append(\"abc\");\n" +
		"    var result: string = strbuf_take();\n" +
		"    print(\"before\");\n" +
		"    print(result);\n" +
		"    print(\"after\");\n" +
		"    return 0;\n" +
		"}\n"
	stdout, exit := compileAndRunArm64(t, src)
	if exit != 0 {
		t.Errorf("exit code: got %d, want 0", exit)
	}
	want := "before\nabc\nafter\n"
	if got := stdout; got != want {
		t.Errorf("stdout mismatch:\n got %q\nwant %q", got, want)
	}
}

// TestArm64StrBufLargeAppend exercises a 4 KiB+ accumulation
// path so the memcpy / bump loop gets stretched beyond a single
// page — guards against regressions in the strbuf_append
// runtime alongside `returnIsString`.
func TestArm64StrBufLargeAppend(t *testing.T) {
	src := "function main(): i32 {\n" +
		"    strbuf_reset();\n" +
		"    var i: i32 = 0;\n" +
		"    while (i < 1000) {\n" +
		"        strbuf_append(\"abcde\");\n" +
		"        i = i + 1;\n" +
		"    }\n" +
		"    var result: string = strbuf_take();\n" +
		"    print(result);\n" +
		"    return 0;\n" +
		"}\n"
	stdout, exit := compileAndRunArm64(t, src)
	if exit != 0 {
		t.Errorf("exit code: got %d, want 0", exit)
	}
	// 1000 × "abcde" + a single "\n" from print.
	wantBody := strings.Repeat("abcde", 1000)
	if !strings.HasPrefix(stdout, wantBody) {
		t.Errorf("body mismatch: got %d bytes (want first %d to be the repeated body)", len(stdout), len(wantBody))
		_ = os.WriteFile(filepath.Join(t.TempDir(), "got.txt"), []byte(stdout), 0o644)
	}
}
