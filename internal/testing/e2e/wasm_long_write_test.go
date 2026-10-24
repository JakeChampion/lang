package e2e

import (
	"strings"
	"testing"
)

func TestWASMLongPrintWriteEprint(t *testing.T) {
	src := `import "std/string";
import "std/i32";

function body(n: i32): string {
    let s: string = "";
    let i: i32 = 0;
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
	out, errOut, ec := runCLIComponent(t, src, runOpts{})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstderr:\n%s", ec, errOut)
	}
	if out != want {
		t.Fatalf("stdout is %d bytes, want the %d expected; first difference near byte %d",
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
