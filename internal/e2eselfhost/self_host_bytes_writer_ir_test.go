package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bytesWriterIRCases exercise std/io_buffered's in-memory BytesWriter through
// the self-host IR path on x86-64 + wasm (the `std/io_buffered` row was fully
// unaudited). The single-program driver resolves no imports and `BytesWriter`
// is a reserved builtin type name, so the surface is inlined verbatim from
// `internal/stdlib/std/io_buffered.fern` with the type renamed to `BW`. This
// verifies the constructs BytesWriter lowers to compile on the IR path: a
// struct with a `u8[]` field, functional struct-spread update appending to that
// array (`BW { ...w, data: … }`), `u8[]` `.append` build with `as u8` casts,
// indexed string-byte reads (write_string's `s[i] as u8`, standing in for the
// real module's `s.bytes()`, which is a std/string method the importless driver
// has no import for), the `string_from_bytes_unchecked` builtin (via `into_string`), and
// `.len()`. Each program
// returns a small deterministic int (<= 126), pinned to the `"ir"` path;
// expectations are oracle-checked against the native interpreter.
// FEATURE-AUDIT std/io_buffered row.
const bytesWriterIRPrelude = `struct BW { data: u8[] }
function bw_new(): BW { var empty: u8[] = []; return BW { data: empty }; }
function (w: BW) write_string(s: string): BW {
    var data: u8[] = w.data;
    var i: i32 = 0;
    while (i < s.len()) { data = data.append(s[i] as u8); i = i + 1; }
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
function (w: BW) is_empty(): boolean { return w.data.len() == 0; }
function (w: BW) into_string(): string { return string_from_bytes_unchecked(w.data); }
function (w: BW) reset(): BW { var empty: u8[] = []; return BW { ...w, data: empty }; }
`

var bytesWriterIRCases = []struct {
	name string
	main string
	want int
}{
	// write_string appends a string's bytes; len reports the running total.
	{"write-string-len", `var w: BW = bw_new(); w = w.write_string("hello"); return w.len();`, 5},
	// write_byte appends one byte; "ab" + 'C' -> len 3.
	{"write-byte", `var w: BW = bw_new(); w = w.write_string("ab"); w = w.write_byte(67); return w.len();`, 3},
	// write_bytes appends a u8[] slice directly: 2 + 2 = 4.
	{"write-bytes", `var w: BW = bw_new(); w = w.write_string("xy"); w = w.write_bytes([1 as u8, 2 as u8]); return w.len();`, 4},
	// into_string round-trips the buffer; first byte of "abC" is 'a' = 97.
	{"into-string", `var w: BW = bw_new(); w = w.write_string("ab"); w = w.write_byte(67); var s: string = w.into_string(); return s[0] as i32;`, 97},
	// into_string preserves a write_byte at the end: last char 'C' = 67.
	{"into-string-tail", `var w: BW = bw_new(); w = w.write_string("ab"); w = w.write_byte(67); var s: string = w.into_string(); return s[s.len() - 1] as i32;`, 67},
	// reset clears the buffer: len back to 0.
	{"reset", `var w: BW = bw_new(); w = w.write_string("abc"); var w2: BW = w.reset(); return w2.len();`, 0},
	// is_empty on a fresh writer -> true -> 1.
	{"is-empty", `var w: BW = bw_new(); if (w.is_empty()) { return 1; } return 0;`, 1},
}

func bytesWriterIRSrc(mainBody string) string {
	return bytesWriterIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostBytesWriterIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
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
		t.Skip("wasmtime not on PATH; skipping self-host bytes_writer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
			watFile := filepath.Join(dir, "bytes_writer_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("bytes_writer wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
