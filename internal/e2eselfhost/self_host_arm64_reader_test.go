package e2eselfhost

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
// The asm_ir_run (-target arm64) driver is built as an x86 host binary (it runs
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")

	// Build the asm_ir_run (-target arm64) driver as an x86 host binary.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
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
		"            None => { r.close(); write(out); return 0; },\n" +
		"        }\n" +
		"    }\n" +
		"    return 0;\n" +
		"}\n"

	// The driver runs on the x86 host; its output is aarch64 asm.
	echoAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(echoSrc), "-target", "arm64")
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

// TestSelfHostReadFileArm64 is the ARM64 counterpart of
// TestSelfHostReadFileX86_64: the self-hosted ARM64 emitter's
// read_file(path) → Result[string, IoError] + Ok/Err match. The
// asm_ir_run (-target arm64) driver (x86 host binary) compiles a "cat" program to
// aarch64 asm; the assembled binary runs under qemu-aarch64 (which
// passes filesystem syscalls through to the host) against an existing
// file (expect its contents, exit 0) and a missing file (exit 7).
func TestSelfHostReadFileArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
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

	catSrc := "function main(): i32 {\n" +
		"    var path: string = arg_at(1);\n" +
		"    match (read_file(path)) {\n" +
		"        Ok(contents) => { write(contents); return 0; },\n" +
		"        Err(e) => { return 7; },\n" +
		"    }\n" +
		"    return 2;\n" +
		"}\n"
	catAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(catSrc), "-target", "arm64")
	if len(catAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the cat program")
	}
	catBin := buildBin(t, arm64gcc, dir, "cat", string(catAsm))

	const contents = "first line\nsecond line\nthird\n"
	srcPath := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(srcPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write input.txt: %v", err)
	}

	t.Run("existing-file", func(t *testing.T) {
		out, err := runArm64Bin(qemu, catBin, srcPath).Output()
		if err != nil {
			t.Fatalf("run cat: %v", err)
		}
		if string(out) != contents {
			t.Errorf("cat mismatch:\n got %q\nwant %q", string(out), contents)
		}
	})
	t.Run("missing-file", func(t *testing.T) {
		cmd := runArm64Bin(qemu, catBin, filepath.Join(dir, "does-not-exist.txt"))
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 7 {
			t.Errorf("cat on missing file exited %d, want 7 (Err arm)", code)
		}
	})
}

// TestSelfHostArgsArm64 gives the ARM64 emitter's args() → string[]
// runtime CI coverage (the x86 side is exercised by the file-driver
// test). The self-hosted ARM64 compiler builds a program that returns
// args().len(); run under qemu-aarch64 with extra arguments, its exit
// code must equal argc.
func TestSelfHostArgsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
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

	argcSrc := "function main(): i32 { return args().len(); }\n"
	progAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(argcSrc), "-target", "arm64")
	if len(progAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the args program")
	}
	progBin := buildBin(t, arm64gcc, dir, "argc", string(progAsm))

	cmd := runArm64Bin(qemu, progBin, "one", "two", "three")
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 4 { // argv[0] + 3 args
		t.Errorf("args().len() returned %d, want 4 (argv0 + 3)", code)
	}
}
