package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostReaderArm64 is the ARM64 counterpart of
// TestSelfHostReaderX86_64: it exercises the self-hosted ARM64
// emitter's Reader / Option lowering (stdin, Reader.read_chunk,
// Reader.close, Some/None construction, match on an Option binding
// the Some payload).
//
// The asm_arm64_run driver is built as an x86 host binary (it runs
// on the test host; only its OUTPUT is aarch64 asm). It compiles a
// read-all-of-stdin echo program to aarch64 asm, which is assembled
// with the aarch64 cross-gcc and run under qemu-aarch64 against
// several stdin inputs — including a >4096-byte input that forces
// the read_chunk loop through multiple Some iterations before None.
//
// SKIPs cleanly when the aarch64 cross-toolchain / qemu-aarch64
// aren't installed (see arm64Tooling); CI provides them.
func TestSelfHostReaderArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Build the asm_arm64_run driver as an x86 host binary.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_arm64_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin := buildBin(t, x86gcc, dir, "driver", asm)

	echoSrc := "function main(): i32 {\n" +
		"    var r: Reader = stdin();\n" +
		"    var out: string = \"\";\n" +
		"    while (true) {\n" +
		"        match (r.read_chunk(4096)) {\n" +
		"            Some(chunk) => { out = out + chunk; },\n" +
		"            None => { r.close(); print(out); return 0; },\n" +
		"        }\n" +
		"    }\n" +
		"    return 0;\n" +
		"}\n"

	// The driver runs on the x86 host; its output is aarch64 asm.
	echoAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(echoSrc))
	if len(echoAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the echo program")
	}
	echoBin := buildBin(t, arm64gcc, dir, "echo", string(echoAsm))

	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"short", "hello reader world"},
		{"with-newlines", "line one\nline two\nline three\n"},
		{"exactly-one-chunk", string(bytes.Repeat([]byte("a"), 4096))},
		{"multi-chunk", string(bytes.Repeat([]byte("xy"), 5000))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if qemu == "" {
				cmd = exec.Command(echoBin)
			} else {
				cmd = exec.Command(qemu, echoBin)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.input))
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("run echo: %v", err)
			}
			if string(out) != tc.input {
				t.Errorf("echo mismatch: got %d bytes, want %d bytes", len(out), len(tc.input))
			}
		})
	}
}
