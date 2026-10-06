package e2eharness

import (
	"os/exec"
	"strings"
	"testing"
)

// Whatever LookupX86_64Tooling hands back must actually target x86-64.
//
// It used to fall back to a bare `gcc` on any host, so on an aarch64 runner
// with qemu-x86_64 installed it reported the HOST compiler as x86-64 tooling.
// The x86 native-link gates then fed it x86-64 asm and got an unknown-mnemonic
// error out of the aarch64 assembler — a failure that reads like a codegen bug
// and is a discovery bug. `-dumpmachine` is the compiler's own answer, so
// this holds on every host rather than only where the mistake shows.
func TestX86_64ToolingTargetsX86_64(t *testing.T) {
	gcc, _, ok := LookupX86_64Tooling()
	if !ok {
		t.Skipf("no x86-64 tooling on this host (gcc=%q)", gcc)
	}
	out, err := exec.Command(gcc, "-dumpmachine").Output()
	if err != nil {
		t.Fatalf("%s -dumpmachine: %v", gcc, err)
	}
	if machine := strings.TrimSpace(string(out)); !strings.HasPrefix(machine, x86MachinePrefix) {
		t.Errorf("%s targets %q, not x86-64 — it cannot assemble what the x86-64 backend emits", gcc, machine)
	}
}
