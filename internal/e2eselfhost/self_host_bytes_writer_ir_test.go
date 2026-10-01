package e2eselfhost

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Exercise the actual module through the primary compiler. The copied BW
// implementation previously left these tests independent of the public API.
// FEATURE-AUDIT std/io_buffered row.
const bytesWriterIRPrelude = `import "std/io_buffered" as io;`

var bytesWriterIRCases = []struct {
	name string
	main string
	want int
}{
	{"write-string-len", `var w = io.bytes_writer_new().write_string("hello"); return w.len();`, 5},
	{"write-byte", `var w = io.bytes_writer_new().write_string("ab").write_byte(67); return w.len();`, 3},
	{"write-bytes", `var w = io.bytes_writer_new().write_string("xy").write_bytes([1 as u8, 2 as u8]); return w.len();`, 4},
	{"into-string", `var w = io.bytes_writer_new().write_string("ab").write_byte(67); match (w.into_string()) { Some(s) => { return s[0] as i32; }, None => { return 1; }, }`, 97},
	{"into-string-tail", `var w = io.bytes_writer_new().write_string("ab").write_byte(67); match (w.into_string()) { Some(s) => { return s[s.len() - 1] as i32; }, None => { return 1; }, }`, 67},
	{"invalid-string", `var w = io.bytes_writer_new().write_byte(255); match (w.into_string()) { Some(_) => { return 1; }, None => { return 42; }, }`, 42},
	{"reset", `var w = io.bytes_writer_new().write_string("abc"); var w2 = w.reset(); return w2.len();`, 0},
	{"is-empty", `var w = io.bytes_writer_new(); if (w.is_empty()) { return 1; } return 0;`, 1},
}

func bytesWriterIRSrc(mainBody string) string {
	return bytesWriterIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostBytesWriterIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range bytesWriterIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, bytesWriterIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}

func TestSelfHostBytesWriterUTF8(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, e2eharness.BytesWriterUTF8Program, target); code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}
