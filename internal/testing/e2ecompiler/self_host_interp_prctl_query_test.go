package e2ecompiler

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The interpreter answers prctl_query with the kernel's result, as the
// compiled program does: option 0 is not a prctl option, so Linux answers
// -EINVAL and the other hosts answer -ENOSYS.
func TestSelfHostInterpPrctlQuery(t *testing.T) {
	const src = `function main(): void {
  print(prctl_query(0));
}
`
	want := "-22\n"
	if runtime.GOOS != "linux" {
		want = "-38\n"
	}
	got := runSelfHostInterp(t, "prctl.fern", src)
	if got != want {
		t.Fatalf("interp printed %q, want %q", got, want)
	}
	host := e2eharness.HostSelfHostTarget()
	bin := e2eharness.CompileSelfHostSource(t, host, src, nil)
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != strings.TrimSpace(want) {
		t.Fatalf("%s binary: err=%v out=%q, want %q", host, err, out, want)
	}
}
