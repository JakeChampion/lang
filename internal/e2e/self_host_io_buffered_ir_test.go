package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ioBufferedIRCases exercise std/io_buffered's in-memory BytesWriter (`data:
// u8[]`) — the value-threaded BUILDER IDIOM (each write returns a new writer and
// the caller rebinds) — through the self-host IR path on x86-64 + wasm (the
// `std/io_buffered` row was fully unaudited). The single-program driver resolves
// no imports and `BytesWriter` is a reserved builtin type name, so the surface is
// inlined from `internal/stdlib/std/io_buffered.fern` with the type renamed to
// `BWrite`. The one faithful deviation: the real `write_string` does
// `s.bytes()` (a std/string method), which the importless driver can't resolve,
// so the inline copy iterates `s[i] as u8` — identical bytes, exercising the same
// `u8[].append` + cast lowering. This verifies the constructs std/io_buffered
// lowers to compile on the IR path: a struct with a `u8[]` field, functional
// struct-spread update (`BWrite { ...w, data: … }`), `u8[]` `.append` build with
// `as u8` element casts, indexed byte reads with `as i32`, the `string_from_bytes`
// builtin, a `boolean`-returning receiver method, and chained struct-returning
// receiver methods. Each program returns a small deterministic int (<= 126),
// pinned to the `"ir"` path; expectations are oracle-checked against the native
// interpreter. FEATURE-AUDIT std/io_buffered row.
const ioBufferedIRPrelude = `struct BWrite { data: u8[] }
function bw_new(): BWrite { var e: u8[] = []; return BWrite { data: e }; }
function (w: BWrite) write_string(s: string): BWrite {
    var data: u8[] = w.data;
    var i: i32 = 0;
    while (i < s.len()) { data = data.append(s[i] as u8); i = i + 1; }
    return BWrite { ...w, data: data };
}
function (w: BWrite) write_bytes(bs: u8[]): BWrite {
    var data: u8[] = w.data;
    var i: i32 = 0;
    while (i < bs.len()) { data = data.append(bs[i]); i = i + 1; }
    return BWrite { ...w, data: data };
}
function (w: BWrite) write_byte(b: i32): BWrite { return BWrite { ...w, data: w.data.append(b as u8) }; }
function (w: BWrite) len(): i32 { return w.data.len(); }
function (w: BWrite) is_empty(): boolean { return w.data.len() == 0; }
function (w: BWrite) into_bytes(): u8[] { return w.data; }
function (w: BWrite) into_string(): string { return string_from_bytes(w.data); }
function (w: BWrite) reset(): BWrite { var e: u8[] = []; return BWrite { ...w, data: e }; }
`

var ioBufferedIRCases = []struct {
	name string
	main string
	want int
}{
	// is_empty() is true for a fresh writer.
	{"is-empty-true", `var w: BWrite = bw_new(); if (w.is_empty()) { return 7; } return 0;`, 7},
	// is_empty() is false after a write.
	{"is-empty-false", `var w: BWrite = bw_new(); w = w.write_byte(65); if (w.is_empty()) { return 0; } return 9;`, 9},
	// write_string appends each byte: "hello" -> len 5.
	{"write-string-len", `var w: BWrite = bw_new(); w = w.write_string("hello"); return w.len();`, 5},
	// write_byte twice -> len 2.
	{"write-byte-len", `var w: BWrite = bw_new(); w = w.write_byte(33); w = w.write_byte(34); return w.len();`, 2},
	// write_bytes copies a slice; into_bytes reads it back: 10+20+30 = 60.
	{"write-bytes-sum", `var w: BWrite = bw_new(); var bs: u8[] = [10 as u8, 20 as u8, 30 as u8]; w = w.write_bytes(bs); var b: u8[] = w.into_bytes(); return b[0] as i32 + b[1] as i32 + b[2] as i32;`, 60},
	// into_string round-trips the buffer: "HTTP/1.1" -> len 8.
	{"into-string-len", `var w: BWrite = bw_new(); w = w.write_string("HTTP/1.1"); return w.into_string().len();`, 8},
	// into_string preserves bytes: first byte of "ABC" is 'A' = 65.
	{"into-string-byte", `var w: BWrite = bw_new(); w = w.write_string("ABC"); var s: string = w.into_string(); return s[0] as i32;`, 65},
	// mixed builder chain: "OK" (2) + '!' (1) + "?" slice (1) -> len 4.
	{"mixed-build", `var w: BWrite = bw_new(); w = w.write_string("OK"); w = w.write_byte(33); var t: u8[] = [63 as u8]; w = w.write_bytes(t); return w.len();`, 4},
	// reset yields an empty writer.
	{"reset-empty", `var w: BWrite = bw_new(); w = w.write_string("xyz"); w = w.reset(); if (w.is_empty()) { return 11; } return 0;`, 11},
	// into_bytes length matches what was written: "abcd" -> 4.
	{"into-bytes-len", `var w: BWrite = bw_new(); w = w.write_string("abcd"); return w.into_bytes().len();`, 4},
}

func ioBufferedIRSrc(mainBody string) string {
	return ioBufferedIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostIoBufferedIRX86_64 routes each case through the self-hosted x86-64
// IR driver, with the routing pinned to the "ir" path.
func TestSelfHostIoBufferedIRX86_64(t *testing.T) {
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

	for _, tc := range ioBufferedIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(ioBufferedIRSrc(tc.main))
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

// TestSelfHostIoBufferedIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostIoBufferedIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host io_buffered wasm IR e2e")
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

	for _, tc := range ioBufferedIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(ioBufferedIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "io_buffered_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("io_buffered wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
