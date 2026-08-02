package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// readerStdinIRCases exercise the built-in Reader resource intrinsics — stdin(),
// r.read_chunk(n), r.close() — newly lowered through the self-host IR path. A
// Reader is just its fd (an i32); stdin() is fd 0; read_chunk(n) reads up to n
// bytes and yields Option[string] (Some(chunk) / None at EOF); close() yields
// Option[IoError]. #2691 routes these (and thus std/io's read_all_stdin loop)
// through IR instead of bailing the whole module to the legacy AST emitter, which
// flips the dominant real-CLI bail (every stdin-reading tool funnels through this
// Reader path). Each case is a single-module program (Reader/stdin/read_chunk are
// builtins) fed a stdin string and oracle-checked against the interpreter with the
// same stdin. x86-64 only — there is no wasm stdin runtime (wasm_eligible rejects
// read_chunk/reader_close, mirroring read_int/read_line).
var readerStdinIRCases = []struct {
	name  string
	main  string
	stdin string
}{
	// Sum the lengths of all chunks = total byte count. "hello" -> 5.
	{"count-bytes", `function main(): i32 { var r: Reader = stdin(); var n: i32 = 0; while (true) { match (r.read_chunk(4096)) { Some(c) => { n = n + c.len(); }, None => { r.close(); return n; } } } return n; }`, "hello"},
	// Empty stdin -> first read is None -> 0.
	{"empty", `function main(): i32 { var r: Reader = stdin(); var n: i32 = 0; while (true) { match (r.read_chunk(4096)) { Some(c) => { n = n + c.len(); }, None => { r.close(); return n; } } } return n; }`, ""},
	// A longer input still totals correctly across one or more chunks. 16 bytes.
	{"longer", `function main(): i32 { var r: Reader = stdin(); var n: i32 = 0; while (true) { match (r.read_chunk(4096)) { Some(c) => { n = n + c.len(); }, None => { r.close(); return n; } } } return n; }`, "hello world test"},
	// A single read_chunk, matched directly (not in a loop): Some -> len, None -> 0.
	{"single-chunk", `function main(): i32 { var r: Reader = stdin(); return match (r.read_chunk(4096)) { Some(c) => c.len(), None => 0 }; }`, "abcd"},
	// close()'s result (Option[IoError]) discarded; just exercise stdin+close. 7.
	{"close-only", `function main(): i32 { var r: Reader = stdin(); r.close(); return 7; }`, "ignored"},
}

func runExitStdin(t *testing.T, bin, in string) int {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(in)
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

func interpExitStdin(t *testing.T, interpBin, src, in string) int {
	t.Helper()
	f := filepath.Join(t.TempDir(), "oracle.fern")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write oracle src: %v", err)
	}
	cmd := exec.Command(interpBin, "-interp", f)
	cmd.Stdin = strings.NewReader(in)
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

// TestSelfHostReaderStdinIRX86_64 routes each direct-Reader case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks the exit
// against the interpreter (same piped stdin).
func TestSelfHostReaderStdinIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stdin-reading driver test runs only natively")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range readerStdinIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExitStdin(t, interpBin, string(src), tc.stdin)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			if code := runExitStdin(t, progBin, tc.stdin); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostReadAllStdinModloadIRX86_64 is the headline multi-module case: a
// program that imports std/io and calls io.read_all_stdin() now compiles through
// the modload IR path (asm_load_run) — verified via -ir-probe reporting
// "module: IR" for the program AND "io__read_all_stdin: ir" — and runs correctly
// against the interpreter oracle with piped stdin.
func TestSelfHostReadAllStdinModloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	const prog = "import \"std/io\";\nfunction main(): i32 { return io.read_all_stdin().len(); }\n"
	for _, in := range []string{"hello", "", "hello world test"} {
		t.Run("len-"+itoaLen(in), func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			// Routing assertion: the program must take the IR path.
			probe, err := exec.Command(mmc, mainPath, stdlibRoot, "-ir-probe").Output()
			if err != nil {
				t.Fatalf("ir-probe: %v", err)
			}
			if !bytes.Contains(probe, []byte("module: IR")) {
				t.Fatalf("read_all_stdin program did not route IR:\n%s", probe)
			}
			// Compile + run with piped stdin, oracle-checked.
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("loader compile: %v", err)
			}
			progBin := buildBin(t, gcc, dir, "readall_"+itoaLen(in), string(asm))
			want := interpExitStdin(t, interpBin, prog, in)
			if code := runExitStdin(t, progBin, in); code != want {
				t.Errorf("read_all_stdin(%q) exited %d, want %d (interp oracle)", in, code, want)
			}
		})
	}
}

func itoaLen(s string) string {
	n := len(s)
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
