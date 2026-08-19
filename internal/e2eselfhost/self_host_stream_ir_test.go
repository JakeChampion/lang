package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// streamIRCases exercise std/stream's in-memory byte Stream — `data: u8[]` +
// `pos` cursor, the value-threaded CURSOR IDIOM (a read returns
// `(value, advancedStream)` and the caller rebinds) — through the self-host IR
// path on x86-64 + wasm (the `std/stream` row was fully unaudited). The
// single-program driver resolves no imports and `Stream` is a reserved builtin
// type name, so the surface is inlined verbatim from
// `internal/stdlib/std/stream.fern` with the type renamed to `Buf`. This
// verifies the constructs std/stream lowers to compile on the IR path: a struct
// with a `u8[]` field + an i32 cursor, functional struct-spread update
// (`Buf { ...s, pos: … }`), tuple-returning methods with pointer + Option
// elements (`(u8[], Buf)`, `(Option[i32], Buf)`, `(Option[string], Buf)`),
// tuple destructuring in `let` (`var (bytes, s2) = s.read_all();`), `u8[]`
// `.append` build with `as u8` element casts, indexed byte reads with `as i32`,
// the `string_from_bytes_unchecked` builtin, `Option` `Some`/`None` with a payload-binding
// `match`, and the read_line CRLF/LF + unterminated-tail logic. Each program
// returns a small deterministic int (<= 126), pinned to the `"ir"` path;
// expectations are oracle-checked against the native interpreter. FEATURE-AUDIT
// std/stream row.
const streamIRPrelude = `struct Buf { data: u8[], pos: i32 }
function buf_from_bytes(bs: u8[]): Buf { return Buf { data: bs, pos: 0 }; }
function (s: Buf) len(): i32 { return s.data.len(); }
function (s: Buf) remaining(): i32 {
    var total: i32 = s.data.len();
    if (s.pos >= total) { return 0; }
    return total - s.pos;
}
function (s: Buf) read_all(): (u8[], Buf) {
    var rem: i32 = s.remaining();
    if (rem == 0) { var empty: u8[] = []; return (empty, s); }
    var out: u8[] = [];
    var i: i32 = s.pos;
    var end: i32 = s.data.len();
    while (i < end) { out = out.append(s.data[i]); i = i + 1; }
    return (out, Buf { ...s, pos: end });
}
function (s: Buf) read_all_string(): (string, Buf) {
    var (bytes, s2) = s.read_all();
    return (string_from_bytes_unchecked(bytes), s2);
}
function (s: Buf) read_byte(): (Option[i32], Buf) {
    if (s.pos >= s.data.len()) { return (None, s); }
    var b: i32 = s.data[s.pos] as i32;
    return (Some(b), Buf { ...s, pos: s.pos + 1 });
}
function (s: Buf) read_n(n: i32): (u8[], Buf) {
    var out: u8[] = [];
    if (n <= 0) { return (out, s); }
    var pos: i32 = s.pos;
    var end: i32 = pos + n;
    if (end > s.data.len()) { end = s.data.len(); }
    while (pos < end) { out = out.append(s.data[pos]); pos = pos + 1; }
    return (out, Buf { ...s, pos: pos });
}
function (s: Buf) read_line(): (Option[string], Buf) {
    if (s.pos >= s.data.len()) { return (None, s); }
    var line_bytes: u8[] = [];
    var pos: i32 = s.pos;
    while (pos < s.data.len()) {
        var b: i32 = s.data[pos] as i32;
        pos = pos + 1;
        if (b == 10) {
            var n: i32 = line_bytes.len();
            if (n > 0) {
                if ((line_bytes[n - 1] as i32) == 13) {
                    var stripped: u8[] = [];
                    var i: i32 = 0;
                    while (i < n - 1) { stripped = stripped.append(line_bytes[i]); i = i + 1; }
                    return (Some(string_from_bytes_unchecked(stripped)), Buf { ...s, pos: pos });
                }
            }
            return (Some(string_from_bytes_unchecked(line_bytes)), Buf { ...s, pos: pos });
        }
        line_bytes = line_bytes.append(b as u8);
    }
    return (Some(string_from_bytes_unchecked(line_bytes)), Buf { ...s, pos: pos });
}
`

var streamIRCases = []struct {
	name string
	main string
	want int
}{
	// .len() reports the backing buffer length independent of the cursor.
	{"len", `var b: Buf = buf_from_bytes([1 as u8, 2 as u8, 3 as u8]); return b.len();`, 3},
	// read_n advances the cursor; remaining() on the returned Buf = 4 - 1 = 3.
	{"remaining", `var b: Buf = buf_from_bytes([1 as u8, 2 as u8, 3 as u8, 4 as u8]); var (out, b2) = b.read_n(1); return b2.remaining();`, 3},
	// read_byte yields Some('A'=65) at the cursor.
	{"read-byte-some", `var b: Buf = buf_from_bytes([65 as u8, 66 as u8]); var (ob, b2) = b.read_byte(); match (ob) { Some(v) => { return v; }, None => { return 0; }, } return 0;`, 65},
	// read_byte on an exhausted Buf yields None.
	{"read-byte-none", `var e: u8[] = []; var b: Buf = buf_from_bytes(e); var (ob, b2) = b.read_byte(); match (ob) { Some(v) => { return 0; }, None => { return 7; }, } return 0;`, 7},
	// read_n(2) returns the first two bytes; 10 + 20 = 30.
	{"read-n", `var b: Buf = buf_from_bytes([10 as u8, 20 as u8, 30 as u8]); var (out, b2) = b.read_n(2); return out[0] as i32 + out[1] as i32;`, 30},
	// read_all_string consumes the remainder as a string: "hi" -> len 2.
	{"read-all-string", `var b: Buf = buf_from_bytes([104 as u8, 105 as u8]); var (s, b2) = b.read_all_string(); return s.len();`, 2},
	// read_line splits on \n: "ab\ncd" -> first line "ab" (len 2).
	{"read-line", `var b: Buf = buf_from_bytes([97 as u8, 98 as u8, 10 as u8, 99 as u8, 100 as u8]); var (line, b2) = b.read_line(); match (line) { Some(l) => { return l.len(); }, None => { return 0; }, } return 0;`, 2},
	// read_line strips a trailing \r before the \n: "x\r\n" -> "x" (len 1).
	{"read-line-crlf", `var b: Buf = buf_from_bytes([120 as u8, 13 as u8, 10 as u8]); var (line, b2) = b.read_line(); match (line) { Some(l) => { return l.len(); }, None => { return 0; }, } return 0;`, 1},
}

func streamIRSrc(mainBody string) string {
	return streamIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostStreamIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, with the routing pinned to the "ir" path.
func TestSelfHostStreamIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range streamIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(streamIRSrc(tc.main))
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

// TestSelfHostStreamIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostStreamIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host stream wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range streamIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(streamIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "stream_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("stream wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
