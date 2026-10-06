package interp

import (
	"runtime"
	"testing"
)

// Under the interpreter the program runs where the compiler runs, so
// `target_arch()` answers with the host, as `target_os()` does. Go and Fern
// spell the two ISAs the corpus runs on differently, and the translation is
// the part worth pinning: a host Go calls "amd64" is "x86-64" here.
func TestTargetArchIsTheHost(t *testing.T) {
	want := runtime.GOARCH
	switch want {
	case "arm64":
		want = "arm64"
	case "amd64":
		want = "x86-64"
	}
	_, stdout := runCapture(t, `function main(): i32 { print(target_arch()); return 0; }`)
	if want += "\n"; stdout != want {
		t.Fatalf("target_arch() printed %q, want the host %q", stdout, want)
	}
}
