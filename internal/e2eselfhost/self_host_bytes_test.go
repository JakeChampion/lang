package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostBytesX86_64 proves the self-hosted x86-64 compiler can
// compile the real std/hex and std/base64 — which build u8[] buffers
// via __alloc_u8, fill them with array index-assignment (arr[i] = v),
// and convert back with string_from_bytes_unchecked. Each module's encode is
// checked against a known vector and round-tripped through decode.
func TestSelfHostBytesX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	cases := []struct {
		mod, input, encoded string
	}{
		{"hex", "Hello, World!", "48656c6c6f2c20576f726c6421"},
		{"base64", "Hello, World!", "SGVsbG8sIFdvcmxkIQ=="},
	}
	for _, tc := range cases {
		t.Run(tc.mod, func(t *testing.T) {
			// encode → write the encoded form, then `|`, then decode.
			mainSrc := "import \"./" + tc.mod + "\";\n" +
				"function main(): i32 {\n" +
				"    var e: string = " + tc.mod + "." + tc.mod + "_encode(\"" + tc.input + "\");\n" +
				"    write(e); write(\"|\");\n" +
				"    write(" + tc.mod + "." + tc.mod + "_decode(e));\n" +
				"    return 0;\n" +
				"}\n"
			asm, progDir := compileStdProgModload(t, runner, driverBin, []string{tc.mod}, mainSrc)
			progBin := buildBin(t, gcc, progDir, tc.mod, asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("run %s: %v", tc.mod, err)
			}
			want := tc.encoded + "|" + tc.input
			if string(out) != want {
				t.Errorf("%s: got %q, want %q", tc.mod, string(out), want)
			}
		})
	}
}

// TestSelfHostBytesArm64 is the ARM64 counterpart (CI-gated, qemu):
// the self-hosted aarch64 compiler compiles real std/hex + std/base64
// and the emitted binaries round-trip a known vector.
func TestSelfHostBytesArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	cases := []struct {
		mod, input, encoded string
	}{
		{"hex", "Hello, World!", "48656c6c6f2c20576f726c6421"},
		{"base64", "Hello, World!", "SGVsbG8sIFdvcmxkIQ=="},
	}
	for _, tc := range cases {
		t.Run(tc.mod, func(t *testing.T) {
			mainSrc := "import \"./" + tc.mod + "\";\n" +
				"function main(): i32 {\n" +
				"    var e: string = " + tc.mod + "." + tc.mod + "_encode(\"" + tc.input + "\");\n" +
				"    write(e); write(\"|\");\n" +
				"    write(" + tc.mod + "." + tc.mod + "_decode(e));\n" +
				"    return 0;\n" +
				"}\n"
			asm, progDir := compileStdProgModload(t, x86runner, driverBin, []string{tc.mod}, mainSrc, "-target", "arm64")
			progBin := buildBin(t, arm64gcc, progDir, tc.mod, asm)
			out, err := runArm64Bin(qemu, progBin).Output()
			if err != nil {
				t.Fatalf("run %s: %v", tc.mod, err)
			}
			want := tc.encoded + "|" + tc.input
			if string(out) != want {
				t.Errorf("%s: got %q, want %q", tc.mod, string(out), want)
			}
		})
	}
}
