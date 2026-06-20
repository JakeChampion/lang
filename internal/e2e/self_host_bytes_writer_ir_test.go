package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bytesWriterIRCases exercise std/io_buffered's in-memory `BytesWriter` — a
// `u8[]`-backed buffer with functional `write_string` / `write_bytes` /
// `write_byte` / `len` / `into_string` — through the self-host IR path on
// x86-64 + wasm (the `std/io_buffered` row was unaudited). The single-program
// driver resolves no imports and `BytesWriter` is a reserved builtin name, so the
// type is inlined as `BW` and `.bytes()` / `string_from_bytes` are treated as
// self-host builtins; this verifies the constructs it lowers to compile on the IR
// path: a struct with a `u8[]` field, functional struct-spread update
// (`BW { ...w, data: … }`), `u8[].append` with `as u8` element casts, `s.bytes()`
// (string → `u8[]`), the `(w) len()` receiver method reading `w.data.len()` (the
// #3478 shape), indexed byte reads, and `string_from_bytes`. Each program returns
// a small deterministic int (kept <= 126), pinned to the `"ir"` path;
// expectations are hardcoded, verified against the native interp + x86-64
// backends. FEATURE-AUDIT std/io_buffered row.
const bytesWriterIRPrelude = `struct BW { data: u8[] }
function bw_new(): BW { var e: u8[] = []; return BW { data: e }; }
function (w: BW) write_string(s: string): BW {
    var bs: u8[] = s.bytes();
    var data: u8[] = w.data;
    var i: i32 = 0;
    while (i < bs.len()) { data = data.append(bs[i]); i = i + 1; }
    return BW { ...w, data: data };
}
function (w: BW) write_bytes(bs: u8[]): BW {
    var data: u8[] = w.data;
    var i: i32 = 0;
    while (i < bs.len()) { data = data.append(bs[i]); i = i + 1; }
    return BW { ...w, data: data };
}
function (w: BW) write_byte(b: i32): BW { return BW { ...w, data: w.data.append(b as u8) }; }
function (w: BW) len(): i32 { return w.data.len(); }
function (w: BW) into_string(): string { return string_from_bytes(w.data); }
`

var bytesWriterIRCases = []struct {
	name string
	main string
	want int
}{
	// "Hello, " (7) + "world" (5) + '!' (1) -> 13 bytes.
	{"len", `var w: BW = bw_new(); w = w.write_string("Hello, "); w = w.write_string("world"); w = w.write_byte(33); return w.len();`, 13},
	// into_string round-trips the same byte count.
	{"into-string-len", `var w: BW = bw_new(); w = w.write_string("Hello, "); w = w.write_string("world"); w = w.write_byte(33); return w.into_string().len();`, 13},
	// a fresh writer is empty.
	{"empty-len", `return bw_new().len();`, 0},
	// write_bytes appends a slice directly.
	{"write-bytes", `var w: BW = bw_new(); var b: u8[] = [1 as u8, 2 as u8, 3 as u8]; w = w.write_bytes(b); return w.len();`, 3},
	// into_string content: "AB"[1] == 'B' (66).
	{"into-string-byte", `var w: BW = bw_new(); w = w.write_string("AB"); return w.into_string()[1];`, 66},
	// repeated write_byte.
	{"write-byte-len", `var w: BW = bw_new(); w = w.write_byte(65); w = w.write_byte(66); return w.len();`, 2},
}

func bytesWriterIRSrc(mainBody string) string {
	return bytesWriterIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostBytesWriterIRX86_64 routes each case through the self-hosted x86-64
// IR driver, pinned to the "ir" path.
func TestSelfHostBytesWriterIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range bytesWriterIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(bytesWriterIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostBytesWriterIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostBytesWriterIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host bytes-writer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range bytesWriterIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(bytesWriterIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "byteswriter_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("bytes-writer wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
