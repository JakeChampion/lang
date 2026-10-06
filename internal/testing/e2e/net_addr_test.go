package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// std/net's address and error layer (#9853) on every backend: the probe
// parses and renders IPv4 and IPv6 text, the bracketed socket-address
// form, the range predicates, IPv4-mapped unwrapping, and the errno table
// the compile target selects. Exit 42 iff every check holds; the printed
// number is the first failing check.
func TestNetAddrInterp(t *testing.T) {
	if out, got := runInterpExitCode(t, e2eharness.NetAddrProbe()); got != 42 {
		t.Fatalf("interp got %d, want 42; first failing check: %s", got, out)
	}
}

func TestNetAddrX86_64(t *testing.T) {
	if out, got := compileAndRunX86_64(t, e2eharness.NetAddrProbe()); got != 42 {
		t.Fatalf("x86-64 got %d, want 42; first failing check: %s", got, out)
	}
}

func TestNetAddrWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, e2eharness.NetAddrProbe()); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestNetAddrArm64(t *testing.T) {
	if out, got := compileAndRunArm64(t, e2eharness.NetAddrProbe()); got != 42 {
		t.Fatalf("arm64 got %d, want 42; first failing check: %s", got, out)
	}
}
