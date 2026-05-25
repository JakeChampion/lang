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

// TestSelfHostReaderX86_64 exercises the self-hosted x86-64 emitter's
// support for the Reader / Option machinery:
//
//   - stdin() → a Reader,
//   - Reader.read_chunk(n) → Some(string) / None,
//   - Reader.close(),
//   - Some(x) / None construction,
//   - match on an Option binding the Some payload.
//
// These are the building blocks std/io.read_all_stdin is written in.
// The test builds the asm_run single-file self-host compiler (Go-built
// x86 host binary), feeds it a hand-written read-all-of-stdin echo
// program, assembles the EMITTED asm into a binary, then runs that
// binary against several stdin inputs — including a >4096-byte input
// that forces the read_chunk loop through multiple Some iterations
// before None — asserting the program echoes its stdin verbatim.
func TestSelfHostReaderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)

	// Build the asm_run driver (lexer + parser + asm) as an x86 host
	// binary: it reads Fern source from stdin and prints x86-64 asm.
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	prog, _, err := modload.Load(filepath.Join(dir, "asm_run.fern"))
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
	driverBin := buildBin(t, gcc, dir, "driver", asm)

	// A read-all-of-stdin echo program — the same shape as
	// std/io.read_all_stdin, with a print to make the result
	// observable.
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

	// stage 1: the self-host compiler emits asm for the echo program.
	echoAsm := runCapture(t, gcc, runner, driverBin, []byte(echoSrc))
	if len(echoAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the echo program")
	}
	echoBin := buildBin(t, gcc, dir, "echo", string(echoAsm))

	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"short", "hello reader world"},
		{"with-newlines", "line one\nline two\nline three\n"},
		{"exactly-one-chunk", string(bytes.Repeat([]byte("a"), 4096))},
		{"multi-chunk", string(bytes.Repeat([]byte("xy"), 5000))}, // 10000 bytes > 4096
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(echoBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], echoBin)...)
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

// TestSelfHostStdIoBundleX86_64 is the capstone of the Reader work:
// the self-hosted compiler compiles the REAL internal/stdlib/std/io.fern
// — unmodified — bundled as a module behind an `import "./io"`, with the
// qualified `io.read_all_stdin()` call rewritten by flatten. This proves
// the emitter handles std/io's actual Reader / Option / match source,
// not just a hand-written reduction of it, so a program can use std/io
// through the self-hosted toolchain.
func TestSelfHostStdIoBundleX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "flatten.fern", "asm.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Build the bundle_run driver as an x86 host binary.
	prog, _, err := modload.Load(filepath.Join(dir, "bundle_run.fern"))
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
	driverBin := buildBin(t, gcc, dir, "driver", asm)

	// Bundle: the unmodified std/io source as module "io", plus a main
	// that imports it and echoes stdin through io.read_all_stdin().
	ioSrc, err := os.ReadFile("../../internal/stdlib/std/io.fern")
	if err != nil {
		t.Fatalf("read std/io.fern: %v", err)
	}
	mainMod := "import \"./io\";\n" +
		"function main(): i32 { write(io.read_all_stdin()); return 0; }\n"
	var bundle bytes.Buffer
	bundle.WriteString("///MODULE io\n")
	bundle.Write(ioSrc)
	bundle.WriteString("\n///MODULE main\n")
	bundle.WriteString(mainMod)

	progAsm := runCapture(t, gcc, runner, driverBin, bundle.Bytes())
	if len(progAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the std/io bundle")
	}
	progBin := buildBin(t, gcc, dir, "ioprog", string(progAsm))

	const input = "compiled real std/io.read_all_stdin via the self-hosted compiler\n"
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(input))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run ioprog: %v", err)
	}
	if string(out) != input {
		t.Errorf("std/io echo mismatch:\n got %q\nwant %q", string(out), input)
	}
}
