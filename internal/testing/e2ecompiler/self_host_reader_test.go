package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostReaderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)

	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	echoSrc := "function main(): i32 {\n" +
		"    let r: Reader = stdin();\n" +
		"    let out: string = \"\";\n" +
		"    while (true) {\n" +
		"        match (r.read_chunk(4096)) {\n" +
		"            Ok(chunk) => {\n" +
		"                if (chunk.len() == 0) { r.close(); write(out); return 0; }\n" +
		"                out = out + chunk;\n" +
		"            },\n" +
		"            Err(e) => { r.close(); write(out); return 0; },\n" +
		"        }\n" +
		"    }\n" +
		"    return 0;\n" +
		"}\n"

	echoAsm := runCapture(t, gcc, runner, driverBin, []byte(echoSrc))
	if len(echoAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the echo program")
	}
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
		{"multi-chunk", string(bytes.Repeat([]byte("xy"), 5000))},
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

func TestSelfHostStdIoBundleX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	mainMod := "import \"std/io\";\n" +
		"function main(): i32 { match (io.read_all_stdin()) { Ok(text) => { write(text); }, Err(_) => { return 253; } } return 0; }\n"
	progAsm, progDir := compileSourceModload(t, runner, driverBin, mainMod)
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

func TestSelfHostReadFileX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("read_file test runs only natively (argv path)")
	}
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	catSrc := "function main(): i32 {\n" +
		"    let path: string = args()[1];\n" +
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
