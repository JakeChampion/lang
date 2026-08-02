package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostFormatBytesIR covers std/format's format_bytes logic through the
// self-hosted x86-64 IR path (a "self-host pending" audit gap). The if-ladder +
// integer division + `i32.to_string()` + string concat all lower through the IR
// path (to_string is a self-host builtin). The stdlib uses `n.abs()` from
// std/i32; the single-program self-host driver can't resolve that import, so a
// free `i32_abs` stands in (the format LOGIC is what's covered). Validated only
// against the self-host -ir path with hardcoded expectations — the native
// compiled compiler needs `import "std/i32"` for `.to_string()`, so it isn't a
// drop-in oracle here.
func TestSelfHostFormatBytesIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	// std/format's format_bytes, with std/i32's n.abs() replaced by a free helper
	// (the single-program driver resolves no imports; to_string is a builtin).
	const helper = `
function i32_abs(n: i32): i32 { if (n < 0) { return 0 - n; } return n; }
function format_bytes(n: i32): string {
    var neg: boolean = (n < 0);
    var mag: i32 = n;
    if (neg) { mag = i32_abs(n); }
    var sign: string = "";
    if (neg) { sign = "-"; }
    if (mag < 1024) { return sign + mag.to_string() + " B"; }
    if (mag < 1024 * 1024) { return sign + (mag / 1024).to_string() + " KiB"; }
    if (mag < 1024 * 1024 * 1024) { return sign + (mag / (1024 * 1024)).to_string() + " MiB"; }
    return sign + (mag / (1024 * 1024 * 1024)).to_string() + " GiB";
}
`
	cases := []struct {
		name string
		src  string
	}{
		// 512 -> "512 B": len 5, "512" + " B".
		{"bytes", helper + `function main(): i32 {
    var s: string = format_bytes(512);
    if (s.len() != 5) { return 100; }
    if (s[0] != 53 || s[1] != 49 || s[2] != 50 || s[3] != 32 || s[4] != 66) { return 101; }
    return 42;
}`},
		// 2048 -> "2 KiB": len 5, '2',' ','K','i','B'.
		{"kib", helper + `function main(): i32 {
    var s: string = format_bytes(2048);
    if (s.len() != 5) { return 100; }
    if (s[0] != 50 || s[2] != 75 || s[3] != 105 || s[4] != 66) { return 101; }
    return 42;
}`},
		// -3*1024*1024 -> "-3 MiB": leading '-', then '3',' ','M'.
		{"neg-mib", helper + `function main(): i32 {
    var s: string = format_bytes(0 - 3145728);
    if (s[0] != 45 || s[1] != 51 || s[2] != 32 || s[3] != 77) { return 100; }
    return 42;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRunIR(t, tc.src); got != 42 {
				t.Errorf("self-host IR format_bytes %q: check = %d, want 42", tc.name, got)
			}
		})
	}
}
