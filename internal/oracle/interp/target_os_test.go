package interp

import (
	"runtime"
	"testing"
)

func TestTargetOSIsTheHost(t *testing.T) {
	_, stdout := runCapture(t, `function main(): i32 { print(target_os()); return 0; }`)
	if want := runtime.GOOS + "\n"; stdout != want {
		t.Fatalf("target_os() printed %q, want the host %q", stdout, want)
	}
}
