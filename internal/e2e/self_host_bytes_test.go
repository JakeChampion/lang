package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// bytesModuleBundle builds a marked ///MODULE bundle of the given
// stdlib module plus a `main` that imports it, ready to feed to a
// bundle_run driver.
func bytesModuleBundle(t *testing.T, mod, mainSrc string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("../../internal/stdlib/std", mod+".fern"))
	if err != nil {
		t.Fatalf("read std/%s.fern: %v", mod, err)
	}
	var b bytes.Buffer
	b.WriteString("///MODULE " + mod + "\n")
	b.Write(src)
	b.WriteString("\n///MODULE main\n")
	b.WriteString(mainSrc)
	return b.Bytes()
}

// TestSelfHostBytesX86_64 proves the self-hosted x86-64 compiler can
// compile the real std/hex and std/base64 — which build u8[] buffers
// via __alloc_u8, fill them with array index-assignment (arr[i] = v),
// and convert back with string_from_bytes. Each module's encode is
// checked against a known vector and round-tripped through decode.
func TestSelfHostBytesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"flatten.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "bundle_run.fern", "driver")

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
			asm := runCapture(t, gcc, runner, driverBin, bytesModuleBundle(t, tc.mod, mainSrc))
			progBin := buildBin(t, gcc, dir, tc.mod, string(asm))
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
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "flatten.fern", "bundle_run_arm64.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "bundle_run_arm64.fern", "driver")

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
			asm := runCapture(t, x86gcc, x86runner, driverBin, bytesModuleBundle(t, tc.mod, mainSrc))
			progBin := buildBin(t, arm64gcc, dir, tc.mod, string(asm))
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
