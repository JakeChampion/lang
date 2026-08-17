package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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
	// Both helpers are Fern runtime functions (#2649), so the calls carry the
	// stack-ABI `__fn___` prefix.
	for _, sym := range []string{"call __fn___fern_reader_read_chunk", "call __fn___fern_reader_close"} {
		if !bytes.Contains(echoAsm, []byte(sym)) {
			t.Fatalf("echo asm has no %q", sym)
		}
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
	gcc, runner, driverBin := buildModloadDriverX86(t)

	// The unmodified std/io source vendored as module "io", plus a main
	// that imports it and echoes stdin through io.read_all_stdin(). The
	// loader vendors std/io.fern as ./io and skips its own unresolved
	// std/ imports — the exact set the ///MODULE bundle hand-picked.
	mainMod := "import \"./io\";\n" +
		"function main(): i32 { write(io.read_all_stdin()); return 0; }\n"
	progAsm, progDir := compileStdProgModload(t, runner, driverBin, []string{"io"}, mainMod)
	if len(progAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the std/io bundle")
	}
	progBin := buildBin(t, gcc, progDir, "ioprog", progAsm)

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

// TestSelfHostReadFileX86_64 exercises the self-hosted x86-64 emitter's
// read_file(path) → Result[string, IoError] support and Ok/Err match.
// The self-hosted compiler builds a "cat" program that reads the file
// named in argv[1] and prints it (Ok) or exits 7 (Err); the emitted
// binary is run against an existing file (expect its contents, exit 0)
// and a missing file (expect exit 7).
func TestSelfHostReadFileX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// The cat program takes a host filesystem path as argv[1];
		// running it under a qemu runner would need the path visible
		// to the emulated process. Skip on the cross-host path.
		t.Skip("read_file test runs only natively (argv path)")
	}
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	// A "cat" program: read argv[1] and print it, or exit 7 on error.
	catSrc := "function main(): i32 {\n" +
		"    var path: string = arg_at(1);\n" +
		"    match (read_file(path)) {\n" +
		"        Ok(contents) => { write(contents); return 0; },\n" +
		"        Err(e) => { return 7; },\n" +
		"    }\n" +
		"    return 2;\n" +
		"}\n"
	catAsm := runCapture(t, gcc, runner, driverBin, []byte(catSrc))
	if len(catAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the cat program")
	}
	catBin := buildBin(t, gcc, dir, "cat", string(catAsm))

	const contents = "first line\nsecond line\nthird\n"
	srcPath := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(srcPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write input.txt: %v", err)
	}

	t.Run("existing-file", func(t *testing.T) {
		cmd := exec.Command(catBin, srcPath)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("run cat: %v", err)
		}
		if string(out) != contents {
			t.Errorf("cat mismatch:\n got %q\nwant %q", string(out), contents)
		}
	})
	t.Run("missing-file", func(t *testing.T) {
		cmd := exec.Command(catBin, filepath.Join(dir, "does-not-exist.txt"))
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 7 {
			t.Errorf("cat on missing file exited %d, want 7 (Err arm)", code)
		}
	})
}
