package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostMangledNames(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("mangled_names_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/mangled_names_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/mangled_names_run.fern", "mangled_names_run")

	const want = "mangled -> json__parse\n" +
		"instance -> Box__i32\n" +
		"instance of array -> Box__arr__i32\n" +
		"reserved -> __memchr\n" +
		"json__parse: mangled=yes reserved=no head=yes tail=yes clone=yes base=json unqualified=parse no-head=json__parse\n" +
		"json__: mangled=yes reserved=no head=yes tail=no clone=no base=json unqualified=json__ no-head=json__\n" +
		"json: mangled=no reserved=no head=no tail=no clone=no base=json unqualified=json no-head=json\n" +
		"jsonx__parse: mangled=yes reserved=no head=no tail=yes clone=no base=jsonx unqualified=jsonx__parse no-head=jsonx__parse\n" +
		"__json__parse: mangled=yes reserved=yes head=no tail=yes clone=no base=__json unqualified=__json__parse no-head=__json__parse\n" +
		"parse: mangled=no reserved=no head=no tail=no clone=no base=parse unqualified=parse no-head=parse\n" +
		"(empty): mangled=no reserved=no head=no tail=no clone=no base= unqualified= no-head=\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("mangled_names_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("mangled names mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
