package interp

import (
	"runtime"
	"testing"
)

// Under the interpreter the program runs where the compiler runs, so
// `target_os()` answers with the host — the one implementation whose
// value is not folded from a `-target` name.
func TestTargetOSIsTheHost(t *testing.T) {
	_, stdout := runCapture(t, `function main(): i32 { print(target_os()); return 0; }`)
	if want := runtime.GOOS + "\n"; stdout != want {
		t.Fatalf("target_os() printed %q, want the host %q", stdout, want)
	}
}
