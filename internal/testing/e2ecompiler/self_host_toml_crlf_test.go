package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostTomlCRLF(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("toml_crlf_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/toml_crlf_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/toml_crlf_run.fern", "toml_crlf_run")

	const want = "LF   name=acme\n" +
		"LF   deps=2\n" +
		"LF   dep dbl kind=path version= path=../dbl\n" +
		"LF   dep shout kind=version version=1.2.3 path=\n" +
		"LF   member a\n" +
		"LF   member b#c\n" +
		"CRLF name=acme\n" +
		"CRLF deps=2\n" +
		"CRLF dep dbl kind=path version= path=../dbl\n" +
		"CRLF dep shout kind=version version=1.2.3 path=\n" +
		"CRLF member a\n" +
		"CRLF member b#c\n" +
		"LF   entries=2\n" +
		"LF   entry dbl version=1.0.0 path=../dbl hash=\n" +
		"LF   entry shout version=1.2.3 path= hash=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
		"CRLF entries=2\n" +
		"CRLF entry dbl version=1.0.0 path=../dbl hash=\n" +
		"CRLF entry shout version=1.2.3 path= hash=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("toml_crlf_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("CRLF manifest/lock parse mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
