package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const stdoutCloseSource = `function main(): i32 {
    let w = stdout();
    match (w.close()) { Err(_) => { eprint("fail 1"); return 1; }, Ok(_) => {} }
    match (w.write("LEAK-a\n")) { Ok(_) => { eprint("fail 2"); return 2; }, Err(_) => {} }
    match (stdout().write("LEAK-b\n")) { Ok(_) => { eprint("fail 3"); return 3; }, Err(_) => {} }
    match (stdout().close()) { Ok(_) => { eprint("fail 4"); return 4; }, Err(_) => {} }
    print("LEAK-c");
    write("LEAK-d");
    putchar(76);
    eprint("done");
    return 0;
}
`

const stderrCloseSource = `function main(): i32 {
    let w = stderr();
    match (w.close()) { Err(_) => { print("fail 1"); return 1; }, Ok(_) => {} }
    match (w.write("LEAK-a\n")) { Ok(_) => { print("fail 2"); return 2; }, Err(_) => {} }
    match (stderr().write("LEAK-b\n")) { Ok(_) => { print("fail 3"); return 3; }, Err(_) => {} }
    match (stderr().close()) { Ok(_) => { print("fail 4"); return 4; }, Err(_) => {} }
    eprint("LEAK-c");
    print("done");
    return 0;
}
`

const stdinCloseSource = `import "std/io";

function main(): i32 {
    let r = stdin();
    match (r.close()) { Err(_) => { print("fail 1"); return 1; }, Ok(_) => {} }
    match (io.read_line(r)) { Ok(_) => { print("fail 2"); return 2; }, Err(_) => {} }
    match (io.read_line(stdin())) { Ok(_) => { print("fail 3"); return 3; }, Err(_) => {} }
    match (stdin().read_chunk(4)) { Ok(_) => { print("fail 4"); return 4; }, Err(_) => {} }
    match (stdin().close()) { Ok(_) => { print("fail 6"); return 6; }, Err(_) => {} }
    print("done");
    return 0;
}
`

type stdioLeg func(t *testing.T, src string) (stdout, stderr string, code int)

func stdioRunCmd(t *testing.T, cmd *exec.Cmd) (string, string, int) {
	t.Helper()
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader("hello\n")
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally\nstdout:\n%s\nstderr:\n%s", so.String(), se.String())
	}
	return so.String(), se.String(), cmd.ProcessState.ExitCode()
}

var stdioLegs = map[string]stdioLeg{
	"interp": func(t *testing.T, src string) (string, string, int) {
		p := filepath.Join(t.TempDir(), "prog.fern")
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return stdioRunCmd(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
	},
	"x86-64": func(t *testing.T, src string) (string, string, int) {
		bin, runner := compileX86_64Bin(t, src)
		return stdioRunCmd(t, runX86_64Bin(runner, bin))
	},
	"wasm preview 1": func(t *testing.T, src string) (string, string, int) {
		mod := buildWasmCore(t, src)
		return stdioRunCmd(t, exec.Command("wasmtime", "run", "--invoke", "main", mod))
	},
	"self-host wasm core": func(t *testing.T, src string) (string, string, int) {
		return runWasmArtifact(t, buildWasmCore(t, src), runOpts{stdin: "hello\n"})
	},
	"self-host wasm": func(t *testing.T, src string) (string, string, int) {
		return runCLIComponent(t, src, runOpts{stdin: "hello\n"})
	},
}

func checkStdioClose(t *testing.T, leg, src, report string) {
	t.Helper()
	stdout, stderr, code := stdioLegs[leg](t, src)
	out := stdout
	if report == "stderr" {
		out = stderr
	}
	if code != 0 || !strings.Contains(out, "done") || strings.Contains(stdout+stderr, "fail") || strings.Contains(stdout+stderr, "LEAK") {
		t.Errorf("%s: exit %d, want 0 with \"done\" on %s and nothing after the close\nstdout:\n%s\nstderr:\n%s", leg, code, report, stdout, stderr)
	}
}

func TestInterpStdoutClose(t *testing.T) { checkStdioClose(t, "interp", stdoutCloseSource, "stderr") }
func TestX86_64StdoutClose(t *testing.T) { checkStdioClose(t, "x86-64", stdoutCloseSource, "stderr") }
func TestWASMPreview1StdoutClose(t *testing.T) {
	checkStdioClose(t, "wasm preview 1", stdoutCloseSource, "stderr")
}

func TestInterpStderrClose(t *testing.T) { checkStdioClose(t, "interp", stderrCloseSource, "stdout") }
func TestX86_64StderrClose(t *testing.T) { checkStdioClose(t, "x86-64", stderrCloseSource, "stdout") }
func TestWASMPreview1StderrClose(t *testing.T) {
	checkStdioClose(t, "wasm preview 1", stderrCloseSource, "stdout")
}

func TestInterpStdinClose(t *testing.T) { checkStdioClose(t, "interp", stdinCloseSource, "stdout") }
func TestX86_64StdinClose(t *testing.T) { checkStdioClose(t, "x86-64", stdinCloseSource, "stdout") }
func TestWASMPreview1StdinClose(t *testing.T) {
	checkStdioClose(t, "wasm preview 1", stdinCloseSource, "stdout")
}

const failureResourceCap = 20

const failedWriteSource = `function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        match (open_writer("full")) {
            Err(_) => { return 1; },
            Ok(w) => {
                match (w.write("x")) { Ok(_) => { return 2; }, Err(_) => {} }
                w.close();
            }
        }
        i = i + 1;
    }
    i = 0;
    while (i < 200) {
        match (open_writer("full")) {
            Err(_) => { return 3; },
            Ok(w) => {
                match (w.write_some("x")) { Ok(_) => { return 4; }, Err(_) => {} }
                w.close();
            }
        }
        i = i + 1;
    }
    return 0;
}
`

const failedReadLineSource = `import "std/io";

function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        match (open_reader("mem")) {
            Err(_) => { return 1; },
            Ok(r) => {
                match (io.read_line(r)) { Ok(_) => { return 2; }, Err(_) => {} }
                r.close();
            }
        }
        i = i + 1;
    }
    return 0;
}
`

const failedReadFileSource = `function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        match (read_file("mem")) { Ok(_) => { return 1; }, Err(_) => {} }
        match (read_file_bytes("mem")) { Ok(_) => { return 2; }, Err(_) => {} }
        i = i + 1;
    }
    return 0;
}
`

const failedWriteFileSource = `function failed(e: IoError): boolean {
    match (e) { Other(_, m, _) => { return m == "No space left on device" || m == "Input/output error"; }, _ => { return false; } }
}
function main(): i32 {
    let bs: u8[] = [120 as u8];
    let i: i32 = 0;
    while (i < 200) {
        match (write_file("full", "x")) { Ok(_) => { return 1; }, Err(e) => { if (!failed(e)) { return 2; } } }
        match (write_file_bytes("full", bs)) { Ok(_) => { return 3; }, Err(e) => { if (!failed(e)) { return 4; } } }
        i = i + 1;
    }
    return 0;
}
`

func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full and /proc/self/mem are Linux's")
	}
}

func devFull(t *testing.T) parityOpts {
	requireLinux(t)
	return parityOpts{dir: "/dev", maxResources: failureResourceCap}
}

func procSelf(t *testing.T) parityOpts {
	requireLinux(t)
	return parityOpts{dir: "/proc/self", maxResources: failureResourceCap}
}

func TestInterpFailedWrite(t *testing.T)       { runParityInterp(t, failedWriteSource, devFull(t)) }
func TestX86_64FailedWrite(t *testing.T)       { runParityX86_64(t, failedWriteSource, devFull(t)) }
func TestWASMPreview1FailedWrite(t *testing.T) { runParityPreview1(t, failedWriteSource, devFull(t)) }

func TestInterpFailedReadLine(t *testing.T) { runParityInterp(t, failedReadLineSource, procSelf(t)) }
func TestX86_64FailedReadLine(t *testing.T) { runParityX86_64(t, failedReadLineSource, procSelf(t)) }
func TestWASMPreview1FailedReadLine(t *testing.T) {
	runParityPreview1(t, failedReadLineSource, procSelf(t))
}

func TestInterpFailedWriteFile(t *testing.T) { runParityInterp(t, failedWriteFileSource, devFull(t)) }
func TestX86_64FailedWriteFile(t *testing.T) { runParityX86_64(t, failedWriteFileSource, devFull(t)) }
func TestWASMPreview1FailedWriteFile(t *testing.T) {
	runParityPreview1(t, failedWriteFileSource, devFull(t))
}
func TestSelfHostWasmFailedWriteFile(t *testing.T) {
	runParitySelfHostComponent(t, failedWriteFileSource, devFull(t))
}

func TestInterpFailedReadFile(t *testing.T) { runParityInterp(t, failedReadFileSource, procSelf(t)) }
func TestX86_64FailedReadFile(t *testing.T) { runParityX86_64(t, failedReadFileSource, procSelf(t)) }
func TestWASMPreview1FailedReadFile(t *testing.T) {
	runParityPreview1(t, failedReadFileSource, procSelf(t))
}

func TestWASMFailedPrint(t *testing.T) {
	requireLinux(t)
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	_, stderr, ec := runWasmArtifact(t, buildCLIComponent(t, failedPrintSource), runOpts{stdoutFile: full, maxResources: failureResourceCap})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d, want 0\nstderr:\n%s", ec, stderr)
	}
}

const failedPrintSource = `function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        print("x");
        write("y");
        putchar(122);
        i = i + 1;
    }
    return 0;
}
`

func TestSelfHostWasmCoreStdoutClose(t *testing.T) {
	checkStdioClose(t, "self-host wasm core", stdoutCloseSource, "stderr")
}
func TestSelfHostWasmStdoutClose(t *testing.T) {
	checkStdioClose(t, "self-host wasm", stdoutCloseSource, "stderr")
}
func TestSelfHostWasmCoreStderrClose(t *testing.T) {
	checkStdioClose(t, "self-host wasm core", stderrCloseSource, "stdout")
}
func TestSelfHostWasmStderrClose(t *testing.T) {
	checkStdioClose(t, "self-host wasm", stderrCloseSource, "stdout")
}
func TestSelfHostWasmCoreStdinClose(t *testing.T) {
	checkStdioClose(t, "self-host wasm core", stdinCloseSource, "stdout")
}
func TestSelfHostWasmStdinClose(t *testing.T) {
	checkStdioClose(t, "self-host wasm", stdinCloseSource, "stdout")
}

func TestSelfHostWasmFailedWrite(t *testing.T) {
	runParitySelfHostComponent(t, failedWriteSource, devFull(t))
}
func TestSelfHostWasmFailedReadLine(t *testing.T) {
	runParitySelfHostComponent(t, failedReadLineSource, procSelf(t))
}
func TestSelfHostWasmFailedReadFile(t *testing.T) {
	runParitySelfHostComponent(t, failedReadFileSource, procSelf(t))
}
