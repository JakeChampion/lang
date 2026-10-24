package e2eharness

import (
	"os/exec"
	"strings"
	"testing"
)

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
