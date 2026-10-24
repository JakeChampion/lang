package interp

import (
	"runtime"
	"testing"
)

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
