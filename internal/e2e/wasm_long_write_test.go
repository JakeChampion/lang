package e2e

import (
	"strings"
	"testing"
)

// Preview 2's blocking-write-and-flush takes at most 4096 bytes a call, so
// print, write and eprint hand a longer string over in pieces. A single call
// with more trapped the host ("Buffer too large for blocking-write-and-flush")
// and the program died mid-output. The lengths straddle the limit and its
// multiples, and each piece boundary lands inside a distinct character run so
// a dropped or repeated piece changes the text (#10061).
func TestWASMLongPrintWriteEprint(t *testing.T) {
	src := `import "std/string";
import "std/i32";

function body(n: i32): string {
    var s: string = "";
    var i: i32 = 0;
    while (s.len() < n) {
        s = s + (i % 10).to_string();
        i = i + 1;
    }
    return s;
}

function main(): i32 {
    write(body(4095) + "|");
    write(body(4096) + "|");
    write(body(4097) + "|");
    print(body(9000));
    eprint(body(8193));
    return 0;
}
`
	body := func(n int) string {
		var sb strings.Builder
		for i := 0; sb.Len() < n; i++ {
			sb.WriteByte(byte('0' + i%10))
		}
		return sb.String()
	}
	want := body(4095) + "|" + body(4096) + "|" + body(4097) + "|" + body(9000) + "\n"
	out, errOut := invokeWasmtime(t, src)
	// invokeWasmtime's result printer appends main's result on its own line.
	if !strings.HasPrefix(out, want) {
		t.Fatalf("stdout is %d bytes and does not start with the %d expected; first difference near byte %d",
			len(out), len(want), firstDiff(out, want))
	}
	if !strings.Contains(errOut, body(8193)+"\n") {
		t.Fatalf("stderr does not hold the 8193-byte eprint line (%d bytes of stderr)", len(errOut))
	}
}

func firstDiff(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
