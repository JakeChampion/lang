package e2eselfhost

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// u8ArrayLit renders a byte string as a Fern `u8[]` literal. The
// encoders take `u8[]` (#5730) and these programs are compiled with only
// the module under test in scope, so std/string's `s.bytes()` is not
// available to convert a literal.
func u8ArrayLit(s string) string {
	parts := make([]string, 0, len(s))
	for i := 0; i < len(s); i++ {
		parts = append(parts, fmt.Sprintf("%d as u8", s[i]))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// TestSelfHostBytesX86_64 proves the self-hosted x86-64 compiler can
// compile the real std/hex and std/base64 — which build u8[] buffers
// via __alloc_u8, fill them with array index-assignment (arr[i] = v),
// and convert back with string_from_bytes_unchecked. Each module's encode is
// checked against a known vector and round-tripped through decode.
func TestSelfHostBytesX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	// hex_decode and base64_decode both yield u8[] (#5730), so each
	// result needs reading back as text before `write`. Note the
	// failure mode if this is wrong: the self-host compiler accepts
	// `write(u8[])` and silently emits NOTHING rather than raising a
	// type error (#5742), so the decode half just vanishes from the
	// output.
	cases := []struct {
		mod, input, encoded, decodeFmt string
	}{
		{"hex", "Hello, World!", "48656c6c6f2c20576f726c6421", "string_from_bytes_unchecked(%s)"},
		{"base64", "Hello, World!", "SGVsbG8sIFdvcmxkIQ==", "string_from_bytes_unchecked(%s)"},
	}
	for _, tc := range cases {
		t.Run(tc.mod, func(t *testing.T) {
			// encode → write the encoded form, then `|`, then decode.
			mainSrc := "import \"./" + tc.mod + "\";\n" +
				"function main(): i32 {\n" +
				"    var e: string = " + tc.mod + "." + tc.mod + "_encode(" + u8ArrayLit(tc.input) + ");\n" +
				"    write(e); write(\"|\");\n" +
				"    write(" + fmt.Sprintf(tc.decodeFmt, tc.mod+"."+tc.mod+"_decode(e)") + ");\n" +
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

	// hex_decode and base64_decode both yield u8[] (#5730), so each
	// result needs reading back as text before `write`. Note the
	// failure mode if this is wrong: the self-host compiler accepts
	// `write(u8[])` and silently emits NOTHING rather than raising a
	// type error (#5742), so the decode half just vanishes from the
	// output.
	cases := []struct {
		mod, input, encoded, decodeFmt string
	}{
		{"hex", "Hello, World!", "48656c6c6f2c20576f726c6421", "string_from_bytes_unchecked(%s)"},
		{"base64", "Hello, World!", "SGVsbG8sIFdvcmxkIQ==", "string_from_bytes_unchecked(%s)"},
	}
	for _, tc := range cases {
		t.Run(tc.mod, func(t *testing.T) {
			mainSrc := "import \"./" + tc.mod + "\";\n" +
				"function main(): i32 {\n" +
				"    var e: string = " + tc.mod + "." + tc.mod + "_encode(" + u8ArrayLit(tc.input) + ");\n" +
				"    write(e); write(\"|\");\n" +
				"    write(" + fmt.Sprintf(tc.decodeFmt, tc.mod+"."+tc.mod+"_decode(e)") + ");\n" +
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
