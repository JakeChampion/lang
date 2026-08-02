package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// i32ToString is the self-host's own integer formatter — it now compiles
// through the SSA pipeline (mod/div, the 10-way digit chain, string concat,
// negative handling), so -ssa can print real numbers.
const i32ToString = `function i32_to_string(n: i32): string {
	if (n == 0) { return "0"; }
	var neg = false;
	if (n < 0) { neg = true; n = 0 - n; }
	var out = "";
	while (n > 0) {
		var d = n % 10;
		var c = "";
		if (d == 0) { c = "0"; } else if (d == 1) { c = "1"; }
		else if (d == 2) { c = "2"; } else if (d == 3) { c = "3"; }
		else if (d == 4) { c = "4"; } else if (d == 5) { c = "5"; }
		else if (d == 6) { c = "6"; } else if (d == 7) { c = "7"; }
		else if (d == 8) { c = "8"; } else { c = "9"; }
		out = c + out;
		n = n / 10;
	}
	if (neg) { out = "-" + out; }
	return out;
}`

// TestSelfHostSSAPrint exercises the SSA backends' `print` op + the
// __fern_ssa_print write-syscall helper: it assembles emitted code and
// captures STDOUT (not just the exit code), asserting the program's output.
// Covers x86-64 (native) and arm64 (under qemu) — the first effectful
// programs the SSA pipeline produces.
func TestSelfHostSSAPrint(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	armgcc, qemu := arm64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_emit_run.fern")
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_emit_run.fern", "ssa_emit_run")

	emit := func(t *testing.T, src string, args ...string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(bin, args...)
		} else {
			cmd = exec.Command(x86runner[0], append(append(x86runner[1:], bin), args...)...)
		}
		cmd.Stdin = strings.NewReader(src)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("emit driver failed for %q: %v", src, err)
		}
		return out
	}

	// Fern's print / eprint append a newline (the native backend, the
	// interpreter, and the AST emitters all do); the SSA backends now match,
	// so each print() call contributes its text followed by '\n'.
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"hello", "function main(): i32 { print(\"hello\"); return 0; }", "hello\n"},
		{"two-prints", "function main(): i32 { print(\"ab\"); print(\"cd\"); return 0; }", "ab\ncd\n"},
		{"print-in-loop", "function main(): i32 { var i = 0; while (i < 3) { print(\"x\"); i = i + 1; } return 0; }", "x\nx\nx\n"},
		{"print-in-for", "function main(): i32 { var a = [1, 2, 3]; for x in a { print(\"y\"); } return 0; }", "y\ny\ny\n"},
		{"print-then-loop", "function main(): i32 { print(\"hi\"); var s = 0; var i = 0; while (i < 5) { s = s + i; i = i + 1; } return s; }", "hi\n"},
		{"print-string-var", "function main(): i32 { var msg = \"done\"; print(msg); return 0; }", "done\n"},
		// String concatenation feeding print.
		{"print-concat", "function main(): i32 { var a = \"foo\"; var b = \"bar\"; print(a + b); return 0; }", "foobar\n"},
		{"print-chained-concat", "function main(): i32 { print(\"a\" + \"b\" + \"c\" + \"d\"); return 0; }", "abcd\n"},
		// A string-returning helper + concat builds output (the i32_to_string shape).
		{"print-built", "function digit(d: i32): string { if (d == 1) { return \"1\"; } if (d == 2) { return \"2\"; } return \"3\"; } function main(): i32 { var out = \"\"; out = out + digit(1); out = out + digit(2); out = out + digit(3); print(out); return 0; }", "123\n"},
		// The headline: a real i32_to_string (mod/div + digit chain + concat)
		// printing formatted numbers — positive, negative, and zero. Each
		// print() adds its own newline, so the explicit print("\n") calls are
		// no longer needed.
		{"print-number", i32ToString + " function main(): i32 { print(i32_to_string(12345)); print(i32_to_string(0 - 67)); print(i32_to_string(0)); return 0; }", "12345\n-67\n0\n"},
		// eprint goes to stderr, so only the print() output lands on stdout.
		{"eprint-separate", "function main(): i32 { print(\"OK\"); eprint(\"ERR\"); print(\"!\"); return 0; }", "OK\n!\n"},
		// write() is print WITHOUT the trailing newline (the driver's raw-output
		// primitive) — __fern_ssa_write. Bytes land verbatim; interleaving with
		// print() shows the newline difference.
		{"write-no-newline", "function main(): i32 { write(\"hi\"); return 0; }", "hi"},
		{"write-multi", "function main(): i32 { write(\"x\"); write(\"y\"); write(\"z\"); return 0; }", "xyz"},
		{"write-then-print", "function main(): i32 { write(\"a\"); print(\"b\"); return 0; }", "ab\n"},
		{"write-concat", "function main(): i32 { var a = \"foo\"; write(a + \"bar\"); return 0; }", "foobar"},
	}

	run := func(t *testing.T, asm []byte, gcc string, pie bool, runner func(string) *exec.Cmd) string {
		t.Helper()
		asmPath := filepath.Join(dir, "p.s")
		binPath := filepath.Join(dir, "p")
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		gccArgs := []string{"-static", "-nostdlib"}
		if pie {
			gccArgs = append(gccArgs, "-no-pie")
		}
		gccArgs = append(gccArgs, asmPath, "-o", binPath)
		if out, err := exec.Command(gcc, gccArgs...).CombinedOutput(); err != nil {
			t.Fatalf("gcc failed: %v\n%s\n--- asm ---\n%s", err, out, asm)
		}
		out, _ := runner(binPath).Output()
		return string(out)
	}

	for _, tc := range cases {
		t.Run("x86_64/"+tc.name, func(t *testing.T) {
			if len(x86runner) != 0 {
				t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
			}
			got := run(t, emit(t, tc.src), x86gcc, true, func(b string) *exec.Cmd { return exec.Command(b) })
			if got != tc.want {
				t.Errorf("x86-64 print of %q = %q, want %q", tc.src, got, tc.want)
			}
		})
		t.Run("arm64/"+tc.name, func(t *testing.T) {
			got := run(t, emit(t, tc.src, "-target", "arm64"), armgcc, false, func(b string) *exec.Cmd { return runArm64Bin(qemu, b) })
			if got != tc.want {
				t.Errorf("arm64 print of %q = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}
