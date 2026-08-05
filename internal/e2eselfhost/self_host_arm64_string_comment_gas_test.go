package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64GasStringComment pins #6082: the in-process GAS parser must
// treat a `//` INSIDE a quoted string as data, not as the start of a line
// comment.
//
// `//` is a line comment in the aarch64 GAS dialect, and the parser stripped it
// with a plain indexof over the whole line. The emitter escapes `"`, `\` and
// control bytes but not `/`, so `.ascii "http://h.com"` was truncated to
// `.ascii "http:` — a five-byte-short literal that shifted every constant after
// it in .rodata. The filed symptom (a `Url` struct read at the wrong offset)
// was a consequence, not the cause; ANY arm64 program with `//` inside a string
// literal was affected, and url_codec was just the first fixture to contain one.
//
// The last case pins the other half of the fix: an unterminated quoted string is
// now REFUSED. Silent truncation is what made this cost a day — the corruption
// surfaces as unrelated constants spliced together, arbitrarily far from the
// literal that caused it.
func TestSelfHostArm64GasStringComment(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 gas string-comment e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64GasStringCommentSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 gas string-comment self-test")
	}
	watPath := filepath.Join(dir, "arm64_gas_string_comment_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 gas string-comment self-test failed at check %d", code)
	}
}

// The assertions read CONTENT, never `data.len()`: the data blob is padded for
// alignment, so its length is not the literal's length (a first cut asserted
// exact lengths and failed on correct output).
const arm64GasStringCommentSelfTestMain = `
function slash_count(d: i32[]): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < d.len()) { if (d[i] == 47) { n = n + 1; } i = i + 1; }
    return n;
}

function main(): i32 {
    // Two slashes inside a .ascii string are DATA. 97 47 47 98 = a / / b.
    var p1: Arm64GasProg = arm64_gas_program(".data\ns:\n.ascii \"a//b\"\n");
    if (p1.data.len() < 4) { return 1; }
    if (p1.data[0] != 97 || p1.data[1] != 47 || p1.data[2] != 47 || p1.data[3] != 98) { return 2; }
    if (slash_count(p1.data) != 2) { return 3; }

    // The url_codec literal that exposed it. The old truncation stopped at
    // "http:" (5 bytes); all 12 must survive — data[7] = 'h' (104), data[11] =
    // 'm' (109) — with both slashes intact at 5 and 6.
    var p2: Arm64GasProg = arm64_gas_program(".data\nu:\n.ascii \"http://h.com\"\n");
    if (p2.data.len() < 12) { return 4; }
    if (p2.data[5] != 47 || p2.data[6] != 47) { return 5; }
    if (p2.data[7] != 104 || p2.data[11] != 109) { return 6; }

    // A comment OUTSIDE a string is still stripped.
    var p3: Arm64GasProg = arm64_gas_program(".data\n.byte 7 // trailing comment\n");
    if (p3.data.len() < 1 || p3.data[0] != 7) { return 7; }
    if (slash_count(p3.data) != 0) { return 8; }

    // ... including one that follows a closing quote on the same line: the
    // comment text must not reach the blob.
    var p4: Arm64GasProg = arm64_gas_program(".data\n.ascii \"ab\" // note\n");
    if (p4.data.len() < 2 || p4.data[0] != 97 || p4.data[1] != 98) { return 9; }
    if (slash_count(p4.data) != 0) { return 10; }

    // An ESCAPED quote does not close the string, so the slashes after it are
    // still data: x " / / y = 120 34 47 47 121.
    var p5: Arm64GasProg = arm64_gas_program(".data\n.ascii \"x\\\"//y\"\n");
    if (p5.data.len() < 5) { return 11; }
    if (p5.data[0] != 120 || p5.data[1] != 34 || p5.data[2] != 47 || p5.data[3] != 47 || p5.data[4] != 121) { return 12; }

    // An UNTERMINATED string is refused rather than silently truncated.
    var p6: Arm64GasProg = arm64_gas_program(".data\n.ascii \"oops\n");
    if (p6.unknown.len() == 0) { return 13; }

    return 0;
}
`
