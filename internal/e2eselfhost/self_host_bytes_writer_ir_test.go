package e2eselfhost

import "testing"

// bytesWriterIRCases exercise std/io_buffered's in-memory BytesWriter through
// the self-host IR path on x86-64 + wasm (the `std/io_buffered` row was fully
// unaudited). `BytesWriter` is a reserved builtin type name, so the surface is
// inlined verbatim from `internal/stdlib/std/io_buffered.fern` with the type
// renamed to `BW`. This verifies the constructs BytesWriter lowers to compile on
// the IR path: a struct with a `u8[]` field, functional struct-spread update
// appending to that array (`BW { ...w, data: … }`), `u8[]` `.append` build with
// `as u8` casts, indexed string-byte reads (write_string's `s[i] as u8`,
// standing in for the real module's `s.bytes()`, a std/string method the
// program does not import), the `string_from_bytes_unchecked` builtin (via
// `into_string`), and `.len()`. Each program returns a small deterministic int
// (<= 126); expectations are oracle-checked against the native interpreter.
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

// TestSelfHostBytesWriterIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostBytesWriterIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range bytesWriterIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, bytesWriterIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
