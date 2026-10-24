package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const streamIRPrelude = `import "std/stream" as stream;`

var streamIRCases = []struct {
	name string
	main string
	want int
}{
	{"len", `let b: Stream = stream.stream_from_bytes([1 as u8, 2 as u8, 3 as u8]); return b.len();`, 3},
	{"bytestream-name", `let b: ByteStream = stream.stream_from_bytes([1 as u8, 2 as u8]); let c: Stream = b; return c.len();`, 2},
	{"remaining", `let b: Stream = stream.stream_from_bytes([1 as u8, 2 as u8, 3 as u8, 4 as u8]); let (out, b2) = b.read_n(1); return b2.remaining();`, 3},
	{"read-byte-some", `let b: Stream = stream.stream_from_bytes([65 as u8, 66 as u8]); let (ob, b2) = b.read_byte(); let byte: Option[u8] = ob; match (byte) { Some(v) => { return v as i32; }, None => { return 0; }, } return 0;`, 65},
	{"read-byte-none", `let e: u8[] = []; let b: Stream = stream.stream_from_bytes(e); let (ob, b2) = b.read_byte(); match (ob) { Some(v) => { return 0; }, None => { return 7; }, } return 0;`, 7},
	{"read-n", `let b: Stream = stream.stream_from_bytes([10 as u8, 20 as u8, 30 as u8]); let (out, b2) = b.read_n(2); return out[0] as i32 + out[1] as i32;`, 30},
	{"read-all-string", `let b: Stream = stream.stream_from_bytes([104 as u8, 105 as u8]); let (s, b2) = b.read_all_string(); match (s) { Some(text) => { return text.len(); }, None => { return 1; }, }`, 2},
	{"read-line", `let b: Stream = stream.stream_from_bytes([97 as u8, 98 as u8, 10 as u8, 99 as u8, 100 as u8]); let (line, b2) = b.read_line(); match (line) { Some(l) => { return l.len(); }, None => { return 0; }, } return 0;`, 2},
	{"read-line-crlf", `let b: Stream = stream.stream_from_bytes([120 as u8, 13 as u8, 10 as u8]); let (line, b2) = b.read_line(); match (line) { Some(l) => { return l.len(); }, None => { return 0; }, } return 0;`, 1},
}

func streamIRSrc(mainBody string) string {
	return streamIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostStreamIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range streamIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, streamIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}

func TestSelfHostStreamUTF8(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, e2eharness.StreamUTF8Program, target); code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}
