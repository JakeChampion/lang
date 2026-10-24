package e2ecompiler

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func u8ArrayLit(s string) string {
	parts := make([]string, 0, len(s))
	for i := 0; i < len(s); i++ {
		parts = append(parts, fmt.Sprintf("%d as u8", s[i]))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func TestSelfHostBytesX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	cases := []struct {
		mod, input, encoded, decodeFmt string
	}{
		{"hex", "Hello, World!", "48656c6c6f2c20576f726c6421", "string_from_bytes_unchecked(%s)"},
		{"base64", "Hello, World!", "SGVsbG8sIFdvcmxkIQ==", "string_from_bytes_unchecked(%s)"},
	}
	for _, tc := range cases {
		t.Run(tc.mod, func(t *testing.T) {
			mainSrc := "import \"std/" + tc.mod + "\";\n" +
				"function main(): i32 {\n" +
				"    let e: string = " + tc.mod + "." + tc.mod + "_encode(" + u8ArrayLit(tc.input) + ");\n" +
				"    write(e); write(\"|\");\n" +
				"    write(" + fmt.Sprintf(tc.decodeFmt, tc.mod+"."+tc.mod+"_decode(e)") + ");\n" +
				"    return 0;\n" +
				"}\n"
			asm, progDir := compileSourceModload(t, runner, driverBin, mainSrc)
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

func TestSelfHostBytesArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	cases := []struct {
		mod, input, encoded, decodeFmt string
	}{
		{"hex", "Hello, World!", "48656c6c6f2c20576f726c6421", "string_from_bytes_unchecked(%s)"},
		{"base64", "Hello, World!", "SGVsbG8sIFdvcmxkIQ==", "string_from_bytes_unchecked(%s)"},
	}
	for _, tc := range cases {
		t.Run(tc.mod, func(t *testing.T) {
			mainSrc := "import \"std/" + tc.mod + "\";\n" +
				"function main(): i32 {\n" +
				"    let e: string = " + tc.mod + "." + tc.mod + "_encode(" + u8ArrayLit(tc.input) + ");\n" +
				"    write(e); write(\"|\");\n" +
				"    write(" + fmt.Sprintf(tc.decodeFmt, tc.mod+"."+tc.mod+"_decode(e)") + ");\n" +
				"    return 0;\n" +
				"}\n"
			asm, progDir := compileSourceModload(t, x86runner, driverBin, mainSrc, "-target", "arm64-linux")
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
